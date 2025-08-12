package claimsponsor

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

type Result struct {
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
}

// GetClaimProof reproduces the logic that existed in BridgeService.
func GetRoots(
	ctx context.Context,
	L1InfoTree *l1infotreesync.L1InfoTreeSync,
	l1InfoIndex uint32,
) (*Result, error) {

	leaf, err := L1InfoTree.GetInfoByIndex(ctx, l1InfoIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get L1 info tree leaf for index %d: %v", l1InfoIndex, err)
	}

	return &Result{
		MainnetExitRoot: leaf.MainnetExitRoot,
		RollupExitRoot:  leaf.RollupExitRoot,
	}, nil
}
