package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type RecoveryPathStore interface {
	ListRecoveryPaths(context.Context) ([]contract.RecoveryPathRecord, error)
	GetRecoveryPath(context.Context, contract.RecoveryPathID) (contract.RecoveryPathRecord, error)
	CreateRecoveryPath(context.Context, contract.RecoveryPath) (contract.RecoveryPathRecord, error)
	UpdateRecoveryPath(context.Context, contract.RecoveryPath, string) (contract.RecoveryPathRecord, error)
	DeleteRecoveryPath(context.Context, contract.RecoveryPathID, string) error
}
