package bridgesync

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	mutex "sync"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/claimsponsor"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/iden3/go-iden3-crypto/keccak256"
	"github.com/russross/meddler"
	_ "modernc.org/sqlite"
)

const (
	// bridgeTableName is the name of the table that stores bridge events
	bridgeTableName = "bridge"

	// claimTableName is the name of the table that stores claim events
	claimTableName = "claim"

	// tokenMappingTableName is the name of the table that stores token mapping events
	tokenMappingTableName = "token_mapping"

	// legacyTokenMigrationTableName is the name of the table that stores legacy token migration events
	legacyTokenMigrationTableName = "legacy_token_migration"

	// Hash length constants
	hashLengthRaw         = 32 // Raw byte length of hash
	hashLengthHex         = 64 // Hex string length without 0x prefix
	hashLengthHexPrefixed = 66 // Hex string length with 0x prefix
)

var (
	// errBlockNotProcessedFormat indicates that the given block(s) have not been processed yet.
	errBlockNotProcessedFormat = fmt.Sprintf("block %%d not processed, last processed: %%d")

	// tableNameRegex is the regex pattern to validate table names
	tableNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

	// deleteLegacyTokenSQL is the SQL statement to delete legacy token migration event
	// with specific legacy token address
	deleteLegacyTokenSQL = fmt.Sprintf("DELETE FROM %s WHERE legacy_token_address = $1", legacyTokenMigrationTableName)
)

// Bridge is the representation of a bridge event
type Bridge struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	FromAddress        common.Address `meddler:"from_address,address"`
	BridgeTxHash       common.Hash    `meddler:"bridge_tx_hash,hash"`
	Calldata           []byte         `meddler:"calldata"`
	BlockTimestamp     uint64         `meddler:"block_timestamp"`
	LeafType           uint8          `meddler:"leaf_type"`
	OriginNetwork      uint32         `meddler:"origin_network"`
	OriginAddress      common.Address `meddler:"origin_address"`
	DestinationNetwork uint32         `meddler:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address"`
	Amount             *big.Int       `meddler:"amount,bigint"`
	Metadata           []byte         `meddler:"metadata"`
	DepositCount       uint32         `meddler:"deposit_count"`
	IsNativeToken      bool           `meddler:"is_native_token"`
}

// Hash returns the hash of the bridge event as expected by the exit tree
// Note: can't change the Hash() here after adding BlockTimestamp and TxHash. Might affect previous versions
func (b *Bridge) Hash() common.Hash {
	const (
		uint32ByteSize = 4
		bigIntSize     = 32
	)
	origNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(origNet, b.OriginNetwork)
	destNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(destNet, b.DestinationNetwork)

	metaHash := keccak256.Hash(b.Metadata)
	var buf [bigIntSize]byte
	if b.Amount == nil {
		b.Amount = common.Big0
	}

	return common.BytesToHash(keccak256.Hash(
		[]byte{b.LeafType},
		origNet,
		b.OriginAddress[:],
		destNet,
		b.DestinationAddress[:],
		b.Amount.FillBytes(buf[:]),
		metaHash,
	))
}

// Claim representation of a claim event
type Claim struct {
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
	FromAddress         common.Address `meddler:"from_address,address"`
	BridgeTxHash        common.Hash    `meddler:"bridge_tx_hash,hash"`
	ClaimTxHash         common.Hash    `meddler:"claim_tx_hash,hash"`
	GlobalIndex         *big.Int       `meddler:"global_index,bigint"`
	OriginNetwork       uint32         `meddler:"origin_network"`
	OriginAddress       common.Address `meddler:"origin_address"`
	DestinationAddress  common.Address `meddler:"destination_address"`
	Amount              *big.Int       `meddler:"amount,bigint"`
	ProofLocalExitRoot  types.Proof    `meddler:"proof_local_exit_root,merkleproof"`
	ProofRollupExitRoot types.Proof    `meddler:"proof_rollup_exit_root,merkleproof"`
	MainnetExitRoot     common.Hash    `meddler:"mainnet_exit_root,hash"`
	RollupExitRoot      common.Hash    `meddler:"rollup_exit_root,hash"`
	GlobalExitRoot      common.Hash    `meddler:"global_exit_root,hash"`
	DestinationNetwork  uint32         `meddler:"destination_network"`
	Metadata            []byte         `meddler:"metadata"`
	IsMessage           bool           `meddler:"is_message"`
	BlockTimestamp      uint64         `meddler:"block_timestamp"`
}

// decodeEtrogCalldataManual manually decodes claim calldata when ABI method lookup fails
// This handles cases where the Go bindings don't match the actual contract methods
func (c *Claim) decodeEtrogCalldataManual(senderAddr common.Address, inputData []byte) (bool, error) {
	// Manual ABI decoding for claimAsset/claimMessage signature:
	// claimAsset/claimMessage(uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)
	// This requires 9 parameters of specific types

	// Expected parameter sizes (in bytes):
	// uint256: 32, bytes32: 32, bytes32: 32, uint32: 32, address: 32, uint32: 32, address: 32, uint256: 32, bytes: variable

	if len(inputData) < 32*8 { // At least 8 fixed parameters * 32 bytes each
		return false, fmt.Errorf("input data too short for manual decode: %d bytes", len(inputData))
	}

	// Parse globalIndex (first parameter)
	globalIndexBytes := inputData[0:32]
	actualGlobalIndex := new(big.Int).SetBytes(globalIndexBytes)

	if actualGlobalIndex.Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	// Parse mainnetExitRoot (second parameter)
	var mainnetExitRoot [32]byte
	copy(mainnetExitRoot[:], inputData[32:64])
	c.MainnetExitRoot = mainnetExitRoot

	// Parse rollupExitRoot (third parameter)
	var rollupExitRoot [32]byte
	copy(rollupExitRoot[:], inputData[64:96])
	c.RollupExitRoot = rollupExitRoot

	// Parse destinationNetwork (sixth parameter, skipping originNetwork and originAddress)
	destinationNetworkBytes := inputData[160:192]
	destinationNetwork := new(big.Int).SetBytes(destinationNetworkBytes).Uint64()
	c.DestinationNetwork = uint32(destinationNetwork)

	// Calculate global exit root
	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())
	c.FromAddress = senderAddr

	return true, nil
}

// decodeEtrogCalldata decodes claim calldata for Etrog fork
func (c *Claim) decodeEtrogCalldata(senderAddr common.Address, data []any) (bool, error) {
	// Unpack method inputs. Note that both claimAsset and claimMessage have the same interface
	// for the relevant parts
	// claimAsset/claimMessage(
	// 	0: globalIndex,
	// 	1: mainnetExitRoot,
	// 	2: rollupExitRoot,
	// 	3: originNetwork,
	// 	4: originTokenAddress/originAddress,
	// 	5: destinationNetwork,
	// 	6: destinationAddress,
	// 	7: amount,
	// 	8: metadata,
	// )

	actualGlobalIndex, ok := data[0].(*big.Int)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected *big.Int got '%T'", data[0])
	}
	if actualGlobalIndex.Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	c.MainnetExitRoot, ok = data[1].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[1])
	}

	c.RollupExitRoot, ok = data[2].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[2])
	}

	c.DestinationNetwork, ok = data[5].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[5])
	}

	c.Metadata, ok = data[8].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'claim Metadata'. Expected '[]byte', got '%T'", data[8])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())
	c.FromAddress = senderAddr

	return true, nil
}

// decodePreEtrogCalldata decodes the claim calldata for pre-Etrog forks
func (c *Claim) decodePreEtrogCalldata(senderAddr common.Address, data []any) (bool, error) {
	// claimMessage/claimAsset(
	// 	0: uint32 index,
	// 	1: bytes32 mainnetExitRoot,
	// 	2: bytes32 rollupExitRoot,
	// 	3: uint32 originNetwork,
	// 	4: address originTokenAddress,
	// 	5: uint32 destinationNetwork,
	// 	6: address destinationAddress,
	// 	7: uint256 amount,
	// 	8: bytes metadata
	// )
	actualGlobalIndex, ok := data[0].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected uint32 got '%T'", data[0])
	}

	if new(big.Int).SetUint64(uint64(actualGlobalIndex)).Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	c.MainnetExitRoot, ok = data[1].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[1])
	}

	c.RollupExitRoot, ok = data[2].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[2])
	}

	c.DestinationNetwork, ok = data[5].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[5])
	}

	c.Metadata, ok = data[8].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'Metadata'. Expected '[]byte', got '%T'", data[8])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())
	c.FromAddress = senderAddr

	return true, nil
}

// TokenMapping representation of a NewWrappedToken event, that is emitted by the bridge contract
type TokenMapping struct {
	BlockNum            uint64                       `meddler:"block_num"`
	BlockPos            uint64                       `meddler:"block_pos"`
	BlockTimestamp      uint64                       `meddler:"block_timestamp"`
	TxHash              common.Hash                  `meddler:"tx_hash,hash"`
	OriginNetwork       uint32                       `meddler:"origin_network"`
	OriginTokenAddress  common.Address               `meddler:"origin_token_address,address"`
	WrappedTokenAddress common.Address               `meddler:"wrapped_token_address,address"`
	Metadata            []byte                       `meddler:"metadata"`
	IsNotMintable       bool                         `meddler:"is_not_mintable"`
	Calldata            []byte                       `meddler:"calldata"`
	Type                bridgetypes.TokenMappingType `meddler:"token_type"`
}

// LegacyTokenMigration representation of a MigrateLegacyToken event,
// that is emitted by the sovereign chain bridge contract.
type LegacyTokenMigration struct {
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
	BlockTimestamp      uint64         `meddler:"block_timestamp"`
	TxHash              common.Hash    `meddler:"tx_hash,hash"`
	Sender              common.Address `meddler:"sender,address"`
	LegacyTokenAddress  common.Address `meddler:"legacy_token_address,address"`
	UpdatedTokenAddress common.Address `meddler:"updated_token_address,address"`
	Amount              *big.Int       `meddler:"amount,bigint"`
	Calldata            []byte         `meddler:"calldata"`
}

// RemoveLegacyToken representation of a RemoveLegacySovereignTokenAddress event,
// that is emitted by the sovereign chain bridge contract.
type RemoveLegacyToken struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	BlockTimestamp     uint64         `meddler:"block_timestamp"`
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
	LegacyTokenAddress common.Address `meddler:"legacy_token_address,address"`
}

// Event combination of bridge, claim, token mapping and legacy token migration events
type Event struct {
	Bridge               *Bridge
	Claim                *Claim
	TokenMapping         *TokenMapping
	LegacyTokenMigration *LegacyTokenMigration
	RemoveLegacyToken    *RemoveLegacyToken
}

type processor struct {
	db           *sql.DB
	exitTree     *tree.AppendOnlyTree
	log          *log.Logger
	mu           mutex.RWMutex
	halted       bool
	haltedReason string
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
	enqueuer  ClaimEnqueuer
	networkId uint32
}

func newProcessor(
	dbPath string,
	name string,
	logger *log.Logger,
	enqueuer ClaimEnqueuer,
	networkId uint32,
) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}

	exitTree := tree.NewAppendOnlyTree(database, "")
	return &processor{
		db:       database,
		exitTree: exitTree,
		log:      logger,
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(database),
			name,
		),
		enqueuer:  enqueuer,
		networkId: networkId,
	}, nil
}

func (p *processor) GetBridges(
	ctx context.Context, fromBlock, toBlock uint64,
) ([]Bridge, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, err
	}
	defer p.rollbackTransaction(tx)

	rows, err := p.queryBlockRange(tx, fromBlock, toBlock, bridgeTableName)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for block range [%d..%d]", fromBlock, toBlock)
			return []Bridge{}, nil
		}
		return nil, err
	}
	bridgePtrs := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridgePtrs); err != nil {
		return nil, err
	}
	bridgesIface := db.SlicePtrsToSlice(bridgePtrs)
	bridges, ok := bridgesIface.([]Bridge)
	if !ok {
		return nil, errors.New("failed to convert from []*Bridge to []Bridge")
	}
	return bridges, nil
}

func (p *processor) GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]Claim, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, err
	}
	defer p.rollbackTransaction(tx)

	rows, err := p.queryBlockRange(tx, fromBlock, toBlock, claimTableName)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no claims were found for block range [%d..%d]", fromBlock, toBlock)
			return []Claim{}, nil
		}
		return nil, err
	}
	claimPtrs := []*Claim{}
	if err = meddler.ScanAll(rows, &claimPtrs); err != nil {
		return nil, err
	}
	claimsIface := db.SlicePtrsToSlice(claimPtrs)
	claims, ok := claimsIface.([]Claim)
	if !ok {
		return nil, errors.New("failed to convert from []*Claim to []Claim")
	}
	return claims, nil
}

func (p *processor) GetBridgesPaged(
	ctx context.Context, pageNumber, pageSize uint32, depositCount *uint64, networkIDs []uint32, fromAddress string,
) ([]*Bridge, int, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, 0, err
	}
	defer p.rollbackTransaction(tx)

	whereClause := p.buildBridgesFilterClause(depositCount, networkIDs, fromAddress)
	orderByClause := "deposit_count DESC"
	bridgesCount, err := p.GetTotalNumberOfRecords(bridgeTableName, whereClause)
	if err != nil {
		return []*Bridge{}, 0, err
	}

	if bridgesCount == 0 {
		return []*Bridge{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, bridgesCount, "bridges")
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPaged(tx, offset, pageSize, bridgeTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for provided parameters (pageNumber=%d, pageSize=%d, where clause=%s)",
				pageNumber, pageSize, whereClause)
			return nil, bridgesCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()

	bridges := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridges); err != nil {
		return nil, 0, err
	}

	return bridges, bridgesCount, nil
}

// buildBridgesFilterClause builds the WHERE clause for the bridges table
// based on the provided depositCount and networkIDs
func (p *processor) buildBridgesFilterClause(depositCount *uint64, networkIDs []uint32, fromAddress string) string {
	const clauseCapacity = 3
	clauses := make([]string, 0, clauseCapacity)
	if depositCount != nil {
		clauses = append(clauses, fmt.Sprintf("deposit_count = %d", *depositCount))
	}

	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "destination_network"))
	}

	if fromAddress != "" && common.IsHexAddress(fromAddress) {
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) = UPPER('%s')", 
			strings.ReplaceAll(fromAddress, "'", "''")))
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND ")
	}
	return ""
}

func (p *processor) GetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32, networkIDs []uint32, fromAddress string,
) ([]*Claim, int, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, 0, err
	}
	defer p.rollbackTransaction(tx)

	whereClause := p.buildClaimsFilterClause(networkIDs, fromAddress)
	claimsCount, err := p.GetTotalNumberOfRecords(claimTableName, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if claimsCount == 0 {
		return []*Claim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, claimsCount, "claims")
	if err != nil {
		return nil, 0, err
	}

	orderByClause := "block_num DESC, block_pos DESC"

	rows, err := p.queryPaged(tx, offset, pageSize, claimTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, claimsCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()

	claims := []*Claim{}
	if err = meddler.ScanAll(rows, &claims); err != nil {
		return nil, 0, err
	}

	return claims, claimsCount, nil
}

// GetPendingClaimsPaged returns bridges that haven't been claimed yet (pending claims)
func (p *processor) GetPendingClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32, networkIDs []uint32, fromAddress string,
) ([]*Bridge, int, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, 0, err
	}
	defer p.rollbackTransaction(tx)

	// Build WHERE clause to find bridges that haven't been claimed
	// We need to find bridges where the global index doesn't exist in the claims table
	whereClause := p.buildPendingClaimsFilterClause(networkIDs, fromAddress)

	// For pending claims, we need bridges that don't have corresponding claims
	// Use a LEFT JOIN to find bridges without claims
	// NOTE: This simplified global index calculation must match the GenerateGlobalIndex function
	query := fmt.Sprintf(`
		SELECT COUNT(DISTINCT b.deposit_count) 
		FROM %s b
		LEFT JOIN %s c ON (
			-- Use the same global index calculation as GenerateGlobalIndex function
			-- For mainnet (origin_network = 0): start with 1 byte (value 1) + 4 zero bytes + 4 bytes for deposit_count
			-- For L2: start with 4 bytes for origin_network + 4 bytes for deposit_count
			CASE 
				WHEN b.origin_network = 0 THEN 
					-- Mainnet: (1 << 64) + deposit_count = 18446744073709551616 + deposit_count
					c.global_index = 18446744073709551616 + b.deposit_count
				ELSE 
					-- L2: (origin_network << 32) + deposit_count  
					c.global_index = (CAST(b.origin_network AS BIGINT) << 32) + b.deposit_count
			END
		)
		WHERE c.global_index IS NULL%s`,
		bridgeTableName, claimTableName, whereClause)

	var pendingClaimsCount int
	err = tx.QueryRow(query).Scan(&pendingClaimsCount)
	if err != nil {
		return nil, 0, err
	}

	if pendingClaimsCount == 0 {
		return []*Bridge{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, pendingClaimsCount, "pending claims")
	if err != nil {
		return nil, 0, err
	}

	// Get the actual pending bridges
	orderByClause := "b.block_num DESC, b.block_pos DESC"

	bridgeQuery := fmt.Sprintf(`
		SELECT b.* 
		FROM %s b
		LEFT JOIN %s c ON (
			-- Use the same global index calculation as GenerateGlobalIndex function
			CASE 
				WHEN b.origin_network = 0 THEN 
					-- Mainnet: (1 << 64) + deposit_count = 18446744073709551616 + deposit_count
					c.global_index = 18446744073709551616 + b.deposit_count
				ELSE 
					-- L2: (origin_network << 32) + deposit_count  
					c.global_index = (CAST(b.origin_network AS BIGINT) << 32) + b.deposit_count
			END
		)
		WHERE c.global_index IS NULL%s
		ORDER BY %s
		LIMIT %d OFFSET %d`,
		bridgeTableName, claimTableName, whereClause, orderByClause, pageSize, offset)

	rows, err := tx.Query(bridgeQuery)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()

	var bridges []*Bridge
	if err = meddler.ScanAll(rows, &bridges); err != nil {
		return nil, 0, err
	}

	return bridges, pendingClaimsCount, nil
}

// buildPendingClaimsFilterClause builds the WHERE clause for finding pending claims
func (p *processor) buildPendingClaimsFilterClause(networkIDs []uint32, fromAddress string) string {
	const clauseCapacity = 2
	clauses := make([]string, 0, clauseCapacity)

	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "b.destination_network"))
	}

	if fromAddress != "" && common.IsHexAddress(fromAddress) {
		clauses = append(clauses, fmt.Sprintf("UPPER(b.from_address) = UPPER('%s')", 
			strings.ReplaceAll(fromAddress, "'", "''")))
	}

	if len(clauses) > 0 {
		return " AND " + strings.Join(clauses, " AND ")
	}
	return ""
}

// buildClaimsFilterClause builds the WHERE clause for the claims table
// based on the provided networkIDs and fromAddress
func (p *processor) buildClaimsFilterClause(networkIDs []uint32, fromAddress string) string {
	const clauseCapacity = 2
	clauses := make([]string, 0, clauseCapacity)
	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "origin_network"))
	}

	if fromAddress != "" && common.IsHexAddress(fromAddress) {
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) = UPPER('%s')", 
			strings.ReplaceAll(fromAddress, "'", "''")))
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND ")
	}
	return ""
}

// GetLegacyTokenMigrations returns the paged legacy token migrations from the database
func (p *processor) GetLegacyTokenMigrations(
	ctx context.Context, pageNumber, pageSize uint32) ([]*LegacyTokenMigration, int, error) {
	whereClause := ""
	legacyTokenMigrationsCount, err := p.GetTotalNumberOfRecords(legacyTokenMigrationTableName, whereClause)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"failed to fetch the total number of %s entries: %w",
			legacyTokenMigrationTableName,
			err,
		)
	}

	if legacyTokenMigrationsCount == 0 {
		return []*LegacyTokenMigration{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, legacyTokenMigrationsCount, "legacy token migrations")
	if err != nil {
		return nil, 0, err
	}

	orderByClause := "block_num DESC, block_pos DESC"
	rows, err := p.queryPaged(p.db, offset, pageSize, legacyTokenMigrationTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no legacy token migrations were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, legacyTokenMigrationsCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()
	tokenMigrations := []*LegacyTokenMigration{}
	if err = meddler.ScanAll(rows, &tokenMigrations); err != nil {
		return nil, 0, err
	}

	return tokenMigrations, legacyTokenMigrationsCount, nil
}

func (p *processor) queryBlockRange(tx dbtypes.Querier, fromBlock, toBlock uint64, table string) (*sql.Rows, error) {
	if err := p.isBlockProcessed(tx, toBlock); err != nil {
		return nil, err
	}
	rows, err := tx.Query(fmt.Sprintf(`
		SELECT * FROM %s
		WHERE block_num >= $1 AND block_num <= $2
		ORDER BY block_num ASC, block_pos ASC;
	`, table), fromBlock, toBlock)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

// queryPaged returns a paged result from the given table
func (p *processor) queryPaged(tx dbtypes.Querier,
	offset, pageSize uint32,
	table, orderByClause, whereClause string,
) (*sql.Rows, error) {
	rows, err := tx.Query(fmt.Sprintf(`
		SELECT *
		FROM %s
		%s
		ORDER BY %s
		LIMIT $1 OFFSET $2;
	`, table, whereClause, orderByClause), pageSize, offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

func (p *processor) isBlockProcessed(tx dbtypes.Querier, blockNum uint64) error {
	lpb, err := p.getLastProcessedBlockWithTx(tx)
	if err != nil {
		return err
	}
	if lpb < blockNum {
		return fmt.Errorf(errBlockNotProcessedFormat, blockNum, lpb)
	}
	return nil
}

// GetLastProcessedBlock returns the last processed block by the processor, including blocks
// that don't have events
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return p.getLastProcessedBlockWithTx(p.db)
}

func (p *processor) getLastProcessedBlockWithTx(tx dbtypes.Querier) (uint64, error) {
	var lastProcessedBlockNum uint64

	row := tx.QueryRow("SELECT num FROM block ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastProcessedBlockNum, err
}

// Reorg triggers a purge and reset process on the processor to leaf it on a state
// as if the last block processed was firstReorgedBlock-1
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		p.log.Errorf("failed to start transaction for reorg: %v", err)
		return err
	}

	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil && !errors.Is(errRllbck, sql.ErrTxDone) {
				p.log.Errorf("error rolling back reorg transaction: %v", errRllbck)
			}
		}
	}()

	res, err := tx.Exec(`DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		p.log.Errorf("failed to delete blocks during reorg: %v", err)
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		p.log.Errorf("failed to get rows affected during reorg: %v", err)
		return err
	}

	if err = p.exitTree.Reorg(tx, firstReorgedBlock); err != nil {
		p.log.Errorf("failed to reorg exit tree: %v", err)
		return err
	}
	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit reorg transaction: %v", err)
		return err
	}

	shouldRollback = false

	sync.UnhaltIfAffectedRows(&p.halted, &p.haltedReason, &p.mu, rowsAffected)
	return nil
}

// ProcessBlock process the events of the block to build the exit tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		p.log.Errorf("processor is halted due to: %s", p.haltedReason)
		return sync.ErrInconsistentState
	}
	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		p.log.Errorf("failed to start transaction for block %d: %v", block.Num, err)
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil && !errors.Is(errRllbck, sql.ErrTxDone) {
				p.log.Errorf("error rolling back db transaction (block number %d): %v", block.Num, errRllbck)
			}
		}
	}()

	// Fast-path: if we already committed this block, skip it.
	last, _ := p.GetLastProcessedBlock(ctx)
	if block.Num <= last {
		return nil
	}

	if _, err := tx.Exec(`INSERT INTO block (num,hash) VALUES ($1,$2)`, block.Num, block.Hash.String()); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			_ = tx.Rollback() // cancel everything else in this tx
			return nil        // duplicate; nothing to do
		}
		return err
	}

	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			p.log.Errorf("failed to convert event to Event type in block %d", block.Num)
			return errors.New("failed to convert sync.Block.Event to Event")
		}

		if event.Bridge != nil {
			if err = p.exitTree.AddLeaf(tx, block.Num, event.Bridge.BlockPos, types.Leaf{
				Index: event.Bridge.DepositCount,
				Hash:  event.Bridge.Hash(),
			}); err != nil {
				if errors.Is(err, tree.ErrInvalidIndex) {
					p.mu.Lock()
					p.halted = true
					p.haltedReason = fmt.Sprintf("error adding leaf to the exit tree: %v", err)
					p.mu.Unlock()
					p.log.Errorf("processor halted: %s", p.haltedReason)
				}
				return sync.ErrInconsistentState
			}
			if err = meddler.Insert(tx, bridgeTableName, event.Bridge); err != nil {
				p.log.Errorf("failed to insert bridge event at block %d: %v", block.Num, err)
				return err
			}
			log.Infof(
				"Bridge event inserted: origin_network=%d, destination_network=%d, deposit_count=%d, tx_hash=%s, networkID=%d",
				event.Bridge.OriginNetwork,
				event.Bridge.DestinationNetwork,
				event.Bridge.DepositCount,
				event.Bridge.BridgeTxHash.Hex(),
				p.networkId,
			)
			if p.enqueuer != nil {
				log.Infof("I entered the if for the adding the claim to the claimsponsor queue")
				claim := bridgeToClaim(event.Bridge)
				if err := p.enqueuer.AddClaimToQueue(claim); err != nil {
					p.log.Errorf("auto-sponsor enqueue failed: %v", err)
				}
			}
			log.Infof("Finished adding the claim to the claimsponsor queue")
		}

		if event.Claim != nil {
			// Populate bridge transaction hash for completed claims
			if event.Claim.BridgeTxHash == (common.Hash{}) {
				p.log.Infof(
					"Looking up bridge tx hash for claim: global_index=%s, origin_network=%d",
					event.Claim.GlobalIndex.String(),
					event.Claim.OriginNetwork,
				)
				if bridgeTxHash, err := p.lookupBridgeTxHashForClaim(tx, event.Claim.GlobalIndex, event.Claim.OriginNetwork); err != nil {
					p.log.Warnf(
						"Failed to lookup bridge tx hash for claim global_index=%s: %v",
						event.Claim.GlobalIndex.String(),
						err,
					)
				} else {
					p.log.Infof("Found bridge tx hash %s for claim global_index=%s", bridgeTxHash.Hex(), event.Claim.GlobalIndex.String())
					event.Claim.BridgeTxHash = bridgeTxHash
				}
			}

			if err = meddler.Insert(tx, claimTableName, event.Claim); err != nil {
				p.log.Errorf("failed to insert claim event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.TokenMapping != nil {
			if err = meddler.Insert(tx, tokenMappingTableName, event.TokenMapping); err != nil {
				p.log.Errorf("failed to insert token mapping event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.LegacyTokenMigration != nil {
			if err = meddler.Insert(tx, legacyTokenMigrationTableName, event.LegacyTokenMigration); err != nil {
				p.log.Errorf("failed to insert legacy token migration event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.RemoveLegacyToken != nil {
			_, err := tx.Exec(deleteLegacyTokenSQL, event.RemoveLegacyToken.LegacyTokenAddress.Hex())
			if err != nil {
				p.log.Errorf("failed to remove legacy token at block %d: %v", block.Num, err)
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit db transaction (block number %d): %v", block.Num, err)
		return err
	}
	shouldRollback = false

	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

// GetTotalNumberOfRecords returns the total number of records in the given table
func (p *processor) GetTotalNumberOfRecords(tableName, whereClause string) (int, error) {
	if !tableNameRegex.MatchString(tableName) {
		return 0, fmt.Errorf("invalid table name '%s' provided", tableName)
	}

	count := 0
	err := p.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) AS count FROM %s%s;`, tableName, whereClause)).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetTokenMappings returns the paged token mappings from the database
func (p *processor) GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32) ([]*TokenMapping, int, error) {
	totalTokenMappings, err := p.GetTotalNumberOfRecords(tokenMappingTableName, "")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", tokenMappingTableName, err)
	}

	if totalTokenMappings == 0 {
		return []*TokenMapping{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, totalTokenMappings, "token mappings")
	if err != nil {
		return nil, 0, err
	}

	tokenMappings, err := p.fetchTokenMappings(ctx, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}

	return tokenMappings, totalTokenMappings, nil
}

// fetchTokenMappings fetches token mappings from the database, based on the provided pagination parameters
func (p *processor) fetchTokenMappings(ctx context.Context, pageSize uint32, offset uint32) ([]*TokenMapping, error) {
	tx, err := p.startTransaction(ctx, true)
	if err != nil {
		return nil, err
	}
	defer p.rollbackTransaction(tx)

	orderByClause := "block_num DESC"
	rows, err := p.queryPaged(tx, offset, pageSize, tokenMappingTableName, orderByClause, "")
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			pageNumber := (offset / pageSize) + 1
			p.log.Debugf("no token mappings were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, nil
		}

		p.log.Errorf("failed to fetch token mappings: %v", err)
		return nil, err
	}

	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()

	tokenMappings := []*TokenMapping{}
	if err = meddler.ScanAll(rows, &tokenMappings); err != nil {
		p.log.Errorf("failed to convert token mappings to the object model: %v", err)
		return nil, err
	}

	return tokenMappings, nil
}

// buildNetworkIDsFilter builds SQL filter for the given network IDs
func buildNetworkIDsFilter(networkIDs []uint32, networkIDColumn string) string {
	placeholders := make([]string, len(networkIDs))
	for i, id := range networkIDs {
		placeholders[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("%s IN (%s)", networkIDColumn, strings.Join(placeholders, ", "))
}

//nolint:unparam
func (p *processor) startTransaction(ctx context.Context, isReadOnly bool) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: isReadOnly})
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (p *processor) rollbackTransaction(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Warnf("error rolling back tx: %v", err)
	}
}

func (p *processor) calculateOffset(pageNumber, pageSize uint32,
	recordsCount int, tableName string) (uint32, error) {
	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(recordsCount) {
		msg := fmt.Sprintf(
			"invalid page number for given page size and total number of %s (page=%d, size=%d, total=%d)",
			tableName,
			pageNumber,
			pageSize,
			recordsCount,
		)
		p.log.Debugf(msg)
		return 0, errors.New(msg)
	}
	return offset, nil
}

func (p *processor) isHalted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.halted
}

func bridgeToClaim(b *Bridge) *claimsponsor.Claim {
	return &claimsponsor.Claim{
		LeafType:           b.LeafType,
		GlobalIndex:        aggkitcommon.GenerateGlobalIndex(b.OriginNetwork == 0, b.OriginNetwork, b.DepositCount),
		OriginNetwork:      b.OriginNetwork,
		OriginTokenAddress: b.OriginAddress,
		DestinationNetwork: b.DestinationNetwork,
		DestinationAddress: b.DestinationAddress,
		Amount:             b.Amount,
		Metadata:           b.Metadata,
		Status:             claimsponsor.PendingClaimStatus,
	}
}

// lookupBridgeTxHashForClaim looks up the bridge transaction hash for a given claim
// by finding the bridge event that matches the claim's global index
func (p *processor) lookupBridgeTxHashForClaim(
	tx dbtypes.Txer,
	globalIndex *big.Int,
	originNetwork uint32,
) (common.Hash, error) {
	// First, try to find the bridge in the current database
	bridgeTxHash, err := p.lookupBridgeInDatabase(tx, globalIndex, originNetwork, "current")
	if err == nil {
		return bridgeTxHash, nil
	}
	p.log.Infof("Bridge not found in current database: %v", err)

	// If not found in current database, try to open and search other bridge databases
	// This handles the case where L1 and L2 syncers use separate databases
	return p.lookupBridgeInOtherDatabases(globalIndex, originNetwork)
}

// lookupBridgeInDatabase searches for a bridge in the specified database
func (p *processor) lookupBridgeInDatabase(
	tx dbtypes.Txer,
	globalIndex *big.Int,
	originNetwork uint32,
	dbName string,
) (common.Hash, error) {
	// First, get total bridge count for debugging
	var totalBridges int
	err := tx.QueryRow("SELECT COUNT(*) FROM bridge").Scan(&totalBridges)
	if err != nil {
		p.log.Warnf("Failed to count bridges in %s database: %v", dbName, err)
	} else {
		p.log.Infof("Total bridges in %s database: %d", dbName, totalBridges)
	}

	// Query bridges that could match this claim
	// We need to check bridges with the same origin network
	query := `
		SELECT bridge_tx_hash, deposit_count, origin_network, block_num, block_pos
		FROM bridge 
		WHERE origin_network = ? 
		ORDER BY block_num DESC, block_pos DESC
		LIMIT 1000`

	rows, err := tx.Query(query, originNetwork)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to query bridges: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Errorf("failed to close rows: %v", err)
		}
	}()

	bridgeCount := 0
	for rows.Next() {
		var bridgeTxHashBytes []byte
		var depositCount uint32
		var bridgeOriginNetwork uint32
		var blockNum uint64
		var blockPos uint64
		bridgeCount++

		if err := rows.Scan(&bridgeTxHashBytes, &depositCount, &bridgeOriginNetwork, &blockNum, &blockPos); err != nil {
			return common.Hash{}, fmt.Errorf("failed to scan bridge row: %w", err)
		}

		// Calculate the global index this bridge would generate
		mainnetFlag := bridgeOriginNetwork == 0
		rollupIndex := bridgeOriginNetwork
		if mainnetFlag {
			rollupIndex = 0
		}
		calculatedGlobalIndex := aggkitcommon.GenerateGlobalIndex(mainnetFlag, rollupIndex, depositCount)

		// Handle bridge transaction hash - check if it's hex-encoded or raw bytes
		var bridgeTxHash common.Hash
		switch len(bridgeTxHashBytes) {
		case hashLengthRaw:
			// Raw bytes - convert directly
			bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
		case hashLengthHex:
			// Hex string without 0x prefix - decode it
			bridgeTxHash = common.HexToHash("0x" + string(bridgeTxHashBytes))
		case hashLengthHexPrefixed:
			if string(bridgeTxHashBytes[:2]) == "0x" {
				// Full hex string with 0x prefix
				bridgeTxHash = common.HexToHash(string(bridgeTxHashBytes))
			} else {
				// Fallback - try direct conversion
				bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
				p.log.Warnf("Unexpected bridge_tx_hash format in %s: length=%d, data=%x", dbName, len(bridgeTxHashBytes), bridgeTxHashBytes)
			}
		default:
			// Fallback - try direct conversion
			bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
			p.log.Warnf(
				"Unexpected bridge_tx_hash format in %s: length=%d, data=%x",
				dbName,
				len(bridgeTxHashBytes),
				bridgeTxHashBytes,
			)
		}

		p.log.Infof(
			"Checking bridge #%d in %s database: tx_hash=%s, origin_network=%d, deposit_count=%d, block_num=%d, block_pos=%d, calculated_global_index=%s, target_global_index=%s",
			bridgeCount,
			dbName,
			bridgeTxHash.Hex(),
			bridgeOriginNetwork,
			depositCount,
			blockNum,
			blockPos,
			calculatedGlobalIndex.String(),
			globalIndex.String(),
		)

		// Check if this matches our claim's global index
		if calculatedGlobalIndex.Cmp(globalIndex) == 0 {
			p.log.Infof("Found matching bridge in %s database! tx_hash=%s", dbName, bridgeTxHash.Hex())
			return bridgeTxHash, nil
		}
	}

	p.log.Infof(
		"Checked %d bridges in %s database for origin_network=%d, none matched global_index=%s",
		bridgeCount,
		dbName,
		originNetwork,
		globalIndex.String(),
	)

	if err := rows.Err(); err != nil {
		return common.Hash{}, fmt.Errorf("error iterating bridge rows: %w", err)
	}

	// No matching bridge found - let's also check if there are any bridges with different origin networks
	// to understand the data better
	var allBridgesQuery = `SELECT COUNT(*), MIN(origin_network), MAX(origin_network) FROM bridge`
	var totalCount, minOrigin, maxOrigin int
	if err := tx.QueryRow(allBridgesQuery).Scan(&totalCount, &minOrigin, &maxOrigin); err == nil {
		p.log.Infof(
			"%s database stats: total_bridges=%d, origin_network_range=[%d,%d]",
			dbName,
			totalCount,
			minOrigin,
			maxOrigin,
		)
	}

	// No matching bridge found in this database
	return common.Hash{}, fmt.Errorf(
		"no bridge found in %s database for global_index %s and origin_network %d",
		dbName,
		globalIndex.String(),
		originNetwork,
	)
}

// lookupBridgeInOtherDatabases attempts to find a bridge in other potential bridge databases
// This handles the case where L1 and L2 syncers use separate databases
func (p *processor) lookupBridgeInOtherDatabases(globalIndex *big.Int, originNetwork uint32) (common.Hash, error) {
	// Try common bridge database paths based on the logs we saw
	candidatePaths := []string{
		"/app/data/bridgel1sync.sqlite",
		"/app/data/bridgel2sync.sqlite",
		"/app/data/bridge.sqlite",
		"/app/data/bridgesync.sqlite",
	}

	for _, dbPath := range candidatePaths {
		if bridgeTxHash, err := p.tryLookupInDatabase(dbPath, globalIndex, originNetwork); err == nil {
			p.log.Infof("Found bridge in external database %s: tx_hash=%s", dbPath, bridgeTxHash.Hex())
			return bridgeTxHash, nil
		} else {
			p.log.Debugf("Failed to find bridge in %s: %v", dbPath, err)
		}
	}

	return common.Hash{}, fmt.Errorf(
		"bridge not found in any accessible database for global_index %s and origin_network %d",
		globalIndex.String(),
		originNetwork,
	)
}

// tryLookupInDatabase attempts to open and search a specific database file
func (p *processor) tryLookupInDatabase(
	dbPath string,
	globalIndex *big.Int,
	originNetwork uint32,
) (common.Hash, error) {
	// Open the database
	otherDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}
	defer func() {
		if err := otherDB.Close(); err != nil {
			log.Errorf("failed to close database: %v", err)
		}
	}()

	// Ping to ensure connection works
	if err := otherDB.Ping(); err != nil {
		return common.Hash{}, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}

	// Check if the bridge table exists
	var tableName string
	err = otherDB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='bridge'").Scan(&tableName)
	if err != nil {
		return common.Hash{}, fmt.Errorf("bridge table not found in %s: %w", dbPath, err)
	}

	// Search for the bridge using direct DB access
	return p.searchBridgeInDB(otherDB, globalIndex, originNetwork, dbPath)
}

// searchBridgeInDB searches for a bridge directly in a database connection
func (p *processor) searchBridgeInDB(
	db *sql.DB,
	globalIndex *big.Int,
	originNetwork uint32,
	dbPath string,
) (common.Hash, error) {
	// First, get total bridge count for debugging
	var totalBridges int
	err := db.QueryRow("SELECT COUNT(*) FROM bridge").Scan(&totalBridges)
	if err != nil {
		p.log.Warnf("Failed to count bridges in %s: %v", dbPath, err)
	} else {
		p.log.Infof("Total bridges in %s: %d", dbPath, totalBridges)
	}

	// Query bridges that could match this claim
	query := `
		SELECT bridge_tx_hash, deposit_count, origin_network, block_num, block_pos
		FROM bridge 
		WHERE origin_network = ? 
		ORDER BY block_num DESC, block_pos DESC
		LIMIT 1000`

	rows, err := db.Query(query, originNetwork)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to query bridges in %s: %w", dbPath, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Errorf("failed to close rows: %v", err)
		}
	}()

	bridgeCount := 0
	for rows.Next() {
		var bridgeTxHashBytes []byte
		var depositCount uint32
		var bridgeOriginNetwork uint32
		var blockNum uint64
		var blockPos uint64
		bridgeCount++

		if err := rows.Scan(&bridgeTxHashBytes, &depositCount, &bridgeOriginNetwork, &blockNum, &blockPos); err != nil {
			return common.Hash{}, fmt.Errorf("failed to scan bridge row in %s: %w", dbPath, err)
		}

		// Calculate the global index this bridge would generate
		mainnetFlag := bridgeOriginNetwork == 0
		rollupIndex := bridgeOriginNetwork
		if mainnetFlag {
			rollupIndex = 0
		}
		calculatedGlobalIndex := aggkitcommon.GenerateGlobalIndex(mainnetFlag, rollupIndex, depositCount)

		// Handle bridge transaction hash - check if it's hex-encoded or raw bytes
		var bridgeTxHash common.Hash
		switch len(bridgeTxHashBytes) {
		case hashLengthRaw:
			// Raw bytes - convert directly
			bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
		case hashLengthHex:
			// Hex string without 0x prefix - decode it
			bridgeTxHash = common.HexToHash("0x" + string(bridgeTxHashBytes))
		case hashLengthHexPrefixed:
			if string(bridgeTxHashBytes[:2]) == "0x" {
				// Full hex string with 0x prefix
				bridgeTxHash = common.HexToHash(string(bridgeTxHashBytes))
			} else {
				// Fallback - try direct conversion
				bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
				p.log.Warnf("Unexpected bridge_tx_hash format in %s: length=%d, data=%x", dbPath, len(bridgeTxHashBytes), bridgeTxHashBytes)
			}
		default:
			// Fallback - try direct conversion
			bridgeTxHash = common.BytesToHash(bridgeTxHashBytes)
			p.log.Warnf(
				"Unexpected bridge_tx_hash format in %s: length=%d, data=%x",
				dbPath,
				len(bridgeTxHashBytes),
				bridgeTxHashBytes,
			)
		}
		p.log.Infof(
			"Checking bridge #%d in %s: tx_hash=%s, origin_network=%d, deposit_count=%d, block_num=%d, block_pos=%d, calculated_global_index=%s, target_global_index=%s",
			bridgeCount,
			dbPath,
			bridgeTxHash.Hex(),
			bridgeOriginNetwork,
			depositCount,
			blockNum,
			blockPos,
			calculatedGlobalIndex.String(),
			globalIndex.String(),
		)

		// Check if this matches our claim's global index
		if calculatedGlobalIndex.Cmp(globalIndex) == 0 {
			p.log.Infof("Found matching bridge in %s! tx_hash=%s", dbPath, bridgeTxHash.Hex())
			return bridgeTxHash, nil
		}
	}

	p.log.Infof(
		"Checked %d bridges in %s for origin_network=%d, none matched global_index=%s",
		bridgeCount,
		dbPath,
		originNetwork,
		globalIndex.String(),
	)

	if err := rows.Err(); err != nil {
		return common.Hash{}, fmt.Errorf("error iterating bridge rows in %s: %w", dbPath, err)
	}

	// No matching bridge found in this database
	return common.Hash{}, fmt.Errorf(
		"no bridge found in %s for global_index %s and origin_network %d",
		dbPath,
		globalIndex.String(),
		originNetwork,
	)
}
