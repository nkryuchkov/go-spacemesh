// TODO(nkryuchkov): move after resolving cyclic dependency
package svmtest

import (
	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/signing"
)

func GenerateAddress(publicKey []byte) types.Address {
	return types.GenerateAddress(publicKey)
}

func GenerateSpawnTransaction(signer *signing.EdSigner, target types.Address) *types.Transaction {
	return &types.Transaction{}
}

func GenerateCallTransaction(signer *signing.EdSigner, target types.Address, nonce, amount, gas, fee uint64) (*types.Transaction, error) {
	inner := types.InnerTransaction{
		AccountNonce: nonce,
		Recipient:    target,
		Amount:       amount,
		GasLimit:     gas,
		Fee:          fee,
	}

	buf, err := types.InterfaceToBytes(&inner)
	if err != nil {
		return nil, err
	}

	sst := &types.Transaction{
		InnerTransaction: inner,
		Signature:        [64]byte{},
	}

	copy(sst.Signature[:], signer.Sign(buf))
	sst.SetOrigin(GenerateAddress(signer.PublicKey().Bytes()))

	return sst, nil
}
