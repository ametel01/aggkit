package aggoracle

import (
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/config/types"
)

type TargetChainType string

const (
	EVMChain TargetChainType = "EVM"
)

var (
	SupportedChainTypes = []TargetChainType{EVMChain}
)

type Config struct {
	TargetChainType TargetChainType `mapstructure:"TargetChainType"`
	URLRPCL1        string          `mapstructure:"URLRPCL1"`
	// BlockFinality indicates the status of the blocks that will be queried in order to sync
	BlockFinality     string                   `mapstructure:"BlockFinality"     jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock"` //nolint:lll
	WaitPeriodNextGER types.Duration           `mapstructure:"WaitPeriodNextGER"`
	EVMSender         chaingersender.EVMConfig `mapstructure:"EVMSender"`
	// SandboxMode enables simplified operation for local development
	SandboxMode bool `mapstructure:"SandboxMode"`
}
