package types

import (
	"runtime"
	"testing"

	"code.cloudfoundry.org/bytefmt"
	"github.com/spacemeshos/go-spacemesh/codec"
	"github.com/spacemeshos/go-spacemesh/signing"
	"github.com/stretchr/testify/assert"
)

func TestProposal_IDSize(t *testing.T) {
	var id ProposalID
	assert.Len(t, id.Bytes(), ProposalIDSize)
}

func TestProposal_Initialize(t *testing.T) {
	p := Proposal{
		InnerProposal: InnerProposal{
			Ballot: *RandomBallot(),
			TxIDs:  []TransactionID{RandomTransactionID(), RandomTransactionID()},
		},
	}
	signer := signing.NewEdSigner()
	p.Ballot.Signature = signer.Sign(p.Ballot.Bytes())
	p.Signature = signer.Sign(p.Bytes())
	assert.NoError(t, p.Initialize())
	assert.NotEqual(t, EmptyProposalID, p.ID())

	err := p.Initialize()
	assert.EqualError(t, err, "proposal already initialized")
}

func TestProposal_Initialize_BadSignature(t *testing.T) {
	p := Proposal{
		InnerProposal: InnerProposal{
			Ballot: *RandomBallot(),
			TxIDs:  []TransactionID{RandomTransactionID(), RandomTransactionID()},
		},
	}
	signer := signing.NewEdSigner()
	p.Ballot.Signature = signer.Sign(p.Ballot.Bytes())
	p.Signature = signer.Sign(p.Bytes())[1:]
	err := p.Initialize()
	assert.EqualError(t, err, "proposal extract key: ed25519: bad signature format")
}

func TestProposal_Initialize_InconsistentBallot(t *testing.T) {
	p := Proposal{
		InnerProposal: InnerProposal{
			Ballot: *RandomBallot(),
			TxIDs:  []TransactionID{RandomTransactionID(), RandomTransactionID()},
		},
	}
	p.Ballot.Signature = signing.NewEdSigner().Sign(p.Ballot.Bytes())
	p.Signature = signing.NewEdSigner().Sign(p.Bytes())
	err := p.Initialize()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inconsistent smesher in proposal")
}

func TestDBProposal(t *testing.T) {
	layer := NewLayerID(100)
	p := GenLayerProposal(layer, RandomTXSet(199))
	assert.Equal(t, layer, p.LayerIndex)
	assert.NotEqual(t, p.ID(), EmptyProposalID)
	assert.NotNil(t, p.SmesherID())
	dbb := &DBProposal{
		ID:         p.ID(),
		BallotID:   p.Ballot.ID(),
		LayerIndex: p.LayerIndex,
		TxIDs:      p.TxIDs,
		Signature:  p.Signature,
	}
	got := dbb.ToProposal(&p.Ballot)
	assert.Equal(t, p, got)

	b := (*Block)(p)
	gotB := dbb.ToBlock()
	assert.NotEqual(t, b, gotB)
	assert.Equal(t, b.ID(), gotB.ID())
	assert.Equal(t, b.LayerIndex, gotB.LayerIndex)
	assert.Equal(t, b.TxIDs, gotB.TxIDs)
	assert.Nil(t, gotB.SmesherID())
}

func Test_decodeMalformedProposal(t *testing.T) {
	malformedProposal := []byte("0000000cfb49fc5e9d4835f7318925b082a9aaa0b82e00329708ee6a9ca167fd75b566e300000001000000402c849c129cc00631e39c5db6847730e8d8a718274ef316c969935d20aa3ae776ef077a16b00ce994b9c2a48c8bcf2444ca550553ba127f61c0ded6e9b5cf9c0e000000000c640ee3b301d3a7e01ba5365a727499308e213700000000000000010c640ee3b301d3a7e01ba5365a727499308e213700000000000000000000000100000032ea560c89006a8b497efa5dcbd30239efa87e433ef15c5785dd077efafc8c8e0087b79cb26b6aa6c8beb3467f4fd31514dc6f91f4a3dcf17c544f4f222a480f45415ce2ec84bf8024d2bc699c18f56dd92e51605757e89e2168bb13bdad47294370fb418d64462d49c736cd720be78ba098957d120df555beec6d16941844b6d1a029868d19eb2d02fb69ed50c84451483fa90f09f1203fd9aa8b160f105a94f1ef2047082020ba10d788940c955d9adcc44cc11ce0b8af3971212907b416810d0a7d8380d07ad6df8a89a42da6ed0af1b5c1af71319dbe44b95a961bb97a632383c8c2b7ff108fcc179f759b8468d14d74e510d6b41077e89c82a84e8421f49a6bcee152422c2f7ff275c92a22c7d4ec510ed23f3db6490fa9348467e47fab7faab97595330af05732280b5a9fee14e6a2fcebc15fbfd63ae9ad0acf371c61d75c45b31b32589a98d28047193424b78d3b9501f9492321deda2bdcdc0ece5cd774b1465196381d917044c0159184578d8a21de1b9e6d4a11e6bcbb4f49aa7ef2a24b27e863a1a45e4d80a3f6dcb08eef715f5609e5bc3e46ac7ee10527027cf6be8e818d83bd78d8accc499548da6bdf4013d456efbb4fd5766c61d599009d746ebae87a83ef834fbfe06b136fe152a7faea811e398de4e6918963ad540a084cf3bbdef5383dceca0470a9e8a5f0ed14594efda4bf5b17c5e48d2437e127ad536134f256c4b01c15b58c6b912b915f212d6f8bbf45ed4d986e1f57a5e53857f4deee359c56001b49b1211f6638ad7c73d391deefa78b6d29ff7e73f796142febda9d9f48be12d9c6b915ea9e6a209d0c948a9f128e241eb7f5b36b92369a611c192a18c3944f990c0bed3d914b283718e57ea90acf12e328cfc22d3c36203006a6c8664db5a4524acb28316006f77b35b82b415f2d50379682d66dd119e35fcf7fe5d3d81b27744302997120ee650526e3f0543adc3f712c0a9d98d6242dcae124d08f6e4593e35cfd8fa2c26e3de9d2b24493380e34d57e93e58e9a479019fd52e7c952073003e67e191d9c6055e3bd258d96ded4afca2abe550cad9e03777a832453b07977aa72cfa38f3fc752957638e693d8f679f750e970d06161ac0749e100863944cbfa5a295fcafcbf5fcb18b41017249c339839b9c8d8d20bca194a2adf07c5e2fa0061121878a04398f983ea2eae78b05014226176c7b65291d1c2a3c44393d185d7f07e24a31b011d59aa59d0a40a3c69c9d6ad0ffd5108d909d0bcc5a580b6458bb8b627611cefa3f43b92cf1b09fd95980104d3adc1d52eaed396cc90c143228e597549358b6ec071d9c77b51afe3f3d731219b0da088ab2a55219473e3bcd299d9a8d342ed94038c3a5c7023c18cfc2a33783a846911e47bf67990e5be635da94065ec58b04dbd2011176b006a6cd1212c009abac09e8c58d7b7db09cd91ef1ca05450c80233453d8e38d688c4f5dff447fcb27388562139ae6d9713c4319281663b980603c601f168e31dcbe775b3ce185204c5e7d277d0d92e617626a74f82dd51564d9f5a3df18c378ee2c4c12037a77a8133e638cae396a4451c482a996b49f22cad0517f9b317a0bcb2ff8be89347a914a884d6d533688dd344f95cfef1157aa1b0b4769354697066c612ec440d2de9b2786e4f2049b56790f6d514ca10983675144b52d8b6d52f038041e9efd3b384284c9c9bb6b84dd2c0364e1709e904af6e85ba3b299446a48eda3a5fc1415ffef0269b06b43381fb49fc5e9d4835f7318925b082a9aaa0b82e00329708ee6a9ca167fd75b566e3f7c9122b9ffad57548853a6a447c251d6b63ba6df97a616eeaed61f94e704dfac2660fb93073164a9b194caa266c66cfe5f9ee1c2815b311fbd3b7a3ed4392039873888e4ded0ee19b051683000b7905de1c94885350319d202f16ba90ca3c840748333c41bad3e950c6b4d151b39934eac896147bd8b5d4672b7200a305f74dce56e49ef6d23d2d792d44dfefca7107b2975a1e8af2a4d1413d24bc604fef1028396465faab9dd279cca969d5d657a510df1ed95ff049dc85002ce78522fa8c3c46f301e00ed5ad4b765223614ab58d36e60615dd1fb7b7c6dd86947bc82f4c75627b50a5cb484dbc0fe44ae0da3a4dd9b198d19837e8ef2b06b4bb4272ed716b7faa529499422af51d546b530d7947c0276a2953201534cf8ebd5a3f9900771546eef361ff6e22edc8d7ae767538b2cf1c5f0062bad689e26ea8ee68dea1ad0000000000000020aeebad4a796fcc2e15dc4c6061b45ed9b373f26adfc798ca7d2d8cc58182718e00000040773d4f64c853f973fc05a3be18e50b556fab3569199b0a752662025335ad53f7434f3fe526af247e17f6722291f78f29d3220281ef745d549ac56091bd959f04")
	var p Proposal

	printMemUsage(t)
	_ = codec.Decode(malformedProposal, &p)
	printMemUsage(t)
}

func printMemUsage(t *testing.T) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	t.Logf("Total cumulative alloc: %v", bytefmt.ByteSize(m.TotalAlloc))
}
