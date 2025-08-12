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
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
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
	TxHash              common.Hash    `meddler:"tx_hash,hash"`
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

func newProcessor(dbPath string, name string, logger *log.Logger, enqueuer ClaimEnqueuer, networkId uint32) (*processor, error) {
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
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) LIKE '%s'", fromAddress))
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
		clauses = append(clauses, fmt.Sprintf("UPPER(b.from_address) LIKE '%s'", fromAddress))
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
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) LIKE '%s'", fromAddress))
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
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", legacyTokenMigrationTableName, err)
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
			log.Infof("Bridge event has been inserted: bridge destination network %d and networkID %d", event.Bridge.DestinationNetwork, p.networkId)
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
		msg := fmt.Sprintf("invalid page number for given page size and total number of %s (page=%d, size=%d, total=%d)",
			tableName, pageNumber, pageSize, recordsCount)
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
