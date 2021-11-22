package miner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spacemeshos/go-spacemesh/blocks"
	"github.com/spacemeshos/go-spacemesh/blocks/mocks"
	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/database"
	dbMocks "github.com/spacemeshos/go-spacemesh/database/mocks"
	"github.com/spacemeshos/go-spacemesh/log/logtest"
	"github.com/spacemeshos/go-spacemesh/mempool"
	"github.com/spacemeshos/go-spacemesh/p2p/pubsub"
	pubsubmocks "github.com/spacemeshos/go-spacemesh/p2p/pubsub/mocks"
	"github.com/spacemeshos/go-spacemesh/rand"
	"github.com/spacemeshos/go-spacemesh/signing"
)

const selectCount = 100

type mockBlockOracle struct {
	calls int
	err   error
	J     uint32
}

func (mbo *mockBlockOracle) BlockEligible(types.LayerID) (types.ATXID, []types.BlockEligibilityProof, []types.ATXID, error) {
	mbo.calls++
	return types.ATXID(types.Hash32{1, 2, 3}), []types.BlockEligibilityProof{{J: mbo.J, Sig: []byte{1}}}, []types.ATXID{atx1, atx2, atx3, atx4, atx5}, mbo.err
}

type mockSyncer struct {
	notSynced bool
}

func (mockSyncer) ListenToGossip() bool {
	return true
}

func (m mockSyncer) IsSynced(context.Context) bool { return !m.notSynced }

type MockProjector struct{}

func (p *MockProjector) GetProjection(types.Address) (nonce uint64, balance uint64, err error) {
	return 1, 1000, nil
}

func init() {
	types.SetLayersPerEpoch(3)
}

var mockProjector = &MockProjector{}

func newPublisher(tb testing.TB) pubsub.Publisher {
	tb.Helper()
	ctrl := gomock.NewController(tb)
	publisher := pubsubmocks.NewMockPublisher(ctrl)
	publisher.EXPECT().
		Publish(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		AnyTimes()
	return publisher
}

func newPublisherWithReceiver(tb testing.TB) (pubsub.Publisher, <-chan []byte) {
	tb.Helper()
	receiver := make(chan []byte, 1024)
	ctrl := gomock.NewController(tb)
	publisher := pubsubmocks.NewMockPublisher(ctrl)
	publisher.EXPECT().
		Publish(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, msg []byte) error {
			receiver <- msg
			return nil
		}).
		AnyTimes()
	return publisher, receiver
}

func TestBlockBuilder_StartStop(t *testing.T) {
	rand.Seed(0)

	txMempool := mempool.NewTxMemPool()

	builder := createBlockBuilder(t, "block-builder", newPublisher(t))
	builder.TransactionPool = txMempool

	err := builder.Start(context.TODO())
	assert.NoError(t, err)

	err = builder.Start(context.TODO())
	assert.Error(t, err)

	err = builder.Close()
	assert.NoError(t, err)

	err = builder.Close()
	assert.Error(t, err)
}

func TestBlockBuilder_createBlockLoop_Beacon(t *testing.T) {
	rand.Seed(0)
	layerID := types.NewLayerID(7)
	epoch := layerID.GetEpoch()

	builder := createBlockBuilder(t, "block-builder", newPublisher(t))
	txMempool := mempool.NewTxMemPool()
	builder.TransactionPool = txMempool

	ctrl := gomock.NewController(t)
	mockTB := mocks.NewMockBeaconGetter(ctrl)
	mockTB.EXPECT().GetBeacon(gomock.Any()).Return(types.HexToHash32("0x94812631").Bytes(), nil).Times(1)
	builder.beaconProvider = mockTB

	mockDB := dbMocks.NewMockDatabase(ctrl)
	mockDB.EXPECT().Put(getEpochKey(epoch), gomock.Any()).Return(nil).Times(1)
	mockDB.EXPECT().Get(getEpochKey(epoch)).Return(nil, database.ErrNotFound).Times(1)
	mockDB.EXPECT().Close().Times(1)
	builder.db = mockDB

	err := builder.Start(context.TODO())
	require.NoError(t, err)

	tx1 := NewTx(t, 1, types.BytesToAddress([]byte{0x01}), signing.NewEdSigner())
	txMempool.Put(tx1.ID(), tx1)

	// causing it to build a block
	builder.beginRoundEvent <- layerID

	err = builder.Close()
	assert.NoError(t, err)
	ctrl.Finish()
}

func TestBlockBuilder_createBlockLoop_NoBeacon(t *testing.T) {
	rand.Seed(0)
	layerID := types.NewLayerID(7)
	epoch := layerID.GetEpoch()

	builder := createBlockBuilder(t, "block-builder", newPublisher(t))
	txMempool := mempool.NewTxMemPool()
	builder.TransactionPool = txMempool

	ctrl := gomock.NewController(t)
	mockTB := mocks.NewMockBeaconGetter(ctrl)
	mockTB.EXPECT().GetBeacon(gomock.Any()).Return(nil, database.ErrNotFound).Times(1)
	builder.beaconProvider = mockTB

	mockDB := dbMocks.NewMockDatabase(ctrl)
	mockDB.EXPECT().Put(getEpochKey(epoch), gomock.Any()).Return(nil).Times(0)
	mockDB.EXPECT().Get(getEpochKey(epoch)).Return(nil, database.ErrNotFound).Times(0)
	mockDB.EXPECT().Close().Times(1)
	builder.db = mockDB

	err := builder.Start(context.TODO())
	assert.NoError(t, err)

	tx1 := NewTx(t, 1, types.BytesToAddress([]byte{0x01}), signing.NewEdSigner())
	txMempool.Put(tx1.ID(), tx1)

	// causing it to build a block
	builder.beginRoundEvent <- layerID

	err = builder.Close()
	assert.NoError(t, err)
	ctrl.Finish()
}

func TestBlockBuilder_BlockIdGeneration(t *testing.T) {
	builder1 := createBlockBuilder(t, "a", newPublisher(t))
	builder2 := createBlockBuilder(t, "b", newPublisher(t))

	atxID1 := types.ATXID(types.HexToHash32("dead"))
	atxID2 := types.ATXID(types.HexToHash32("beef"))

	beacon := types.HexToHash32("0x94812631").Bytes()
	b1, err := builder1.createBlock(context.TODO(), types.GetEffectiveGenesis().Add(2), atxID1, types.BlockEligibilityProof{}, nil, nil, beacon)
	assert.NoError(t, err)
	b2, err := builder2.createBlock(context.TODO(), types.GetEffectiveGenesis().Add(2), atxID2, types.BlockEligibilityProof{}, nil, nil, beacon)
	assert.NoError(t, err)

	assert.NotEqual(t, b1.ID(), b2.ID(), "ids are identical")
}

var (
	block1 = types.NewExistingBlock(types.LayerID{}, []byte(rand.String(8)), nil)
	block2 = types.NewExistingBlock(types.LayerID{}, []byte(rand.String(8)), nil)
	block3 = types.NewExistingBlock(types.LayerID{}, []byte(rand.String(8)), nil)
	block4 = types.NewExistingBlock(types.LayerID{}, []byte(rand.String(8)), nil)
)

func prepareBuildingBlocks(t *testing.T) (*mempool.TxMempool, []types.TransactionID) {
	recipient := types.BytesToAddress([]byte{0x01})
	signer := signing.NewEdSigner()
	txPool := mempool.NewTxMemPool()
	trans := []*types.Transaction{
		NewTx(t, 1, recipient, signer),
		NewTx(t, 2, recipient, signer),
		NewTx(t, 3, recipient, signer),
	}
	txIDs := []types.TransactionID{trans[0].ID(), trans[1].ID(), trans[2].ID()}
	txPool.Put(trans[0].ID(), trans[0])
	txPool.Put(trans[1].ID(), trans[1])
	txPool.Put(trans[2].ID(), trans[2])

	return txPool, txIDs
}

func TestBlockBuilder_CreateBlockFlow(t *testing.T) {
	beginRound := make(chan types.LayerID)
	publisher, receiver := newPublisherWithReceiver(t)

	txPool, txIDs := prepareBuildingBlocks(t)

	builder := createBlockBuilder(t, "a", publisher)
	blockset := []types.BlockID{block1.ID(), block2.ID(), block3.ID()}
	builder.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{}, blockset, {}}, nil
	}}
	builder.TransactionPool = txPool
	builder.beginRoundEvent = beginRound
	beacon := types.HexToHash32("0x94812631").Bytes()
	ctrl := gomock.NewController(t)
	mockTB := mocks.NewMockBeaconGetter(ctrl)
	mockTB.EXPECT().GetBeacon(gomock.Any()).Return(beacon, nil).Times(1)
	builder.beaconProvider = mockTB
	require.NoError(t, builder.Start(context.TODO()))

	go func() { beginRound <- types.GetEffectiveGenesis().Add(1) }()
	select {
	case output := <-receiver:
		b := types.MiniBlock{}
		require.NoError(t, types.BytesToInterface(output, &b))

		assert.Equal(t, []types.BlockID{block1.ID(), block2.ID(), block3.ID()}, b.ForDiff)

		assert.True(t, ContainsTx(b.TxIDs, txIDs[0]))
		assert.True(t, ContainsTx(b.TxIDs, txIDs[1]))
		assert.True(t, ContainsTx(b.TxIDs, txIDs[2]))

		assert.Equal(t, []types.ATXID{atx1, atx2, atx3, atx4, atx5}, *b.ActiveSet)
		assert.Equal(t, beacon, b.TortoiseBeacon)
	case <-time.After(500 * time.Millisecond):
		assert.Fail(t, "timeout on receiving block")
	}

	ctrl.Finish()
}

func TestBlockBuilder_CreateBlockFlowNoATX(t *testing.T) {
	beginRound := make(chan types.LayerID)
	publisher, receiver := newPublisherWithReceiver(t)

	txPool, _ := prepareBuildingBlocks(t)

	builder := createBlockBuilder(t, "a", publisher)
	blockset := []types.BlockID{block1.ID(), block2.ID(), block3.ID()}
	builder.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{}, blockset, {}}, nil
	}}
	builder.TransactionPool = txPool
	builder.beginRoundEvent = beginRound

	beacon := types.HexToHash32("0x94812631").Bytes()
	ctrl := gomock.NewController(t)
	mockTB := mocks.NewMockBeaconGetter(ctrl)
	mockTB.EXPECT().GetBeacon(gomock.Any()).Return(beacon, nil).Times(1)
	builder.beaconProvider = mockTB

	mbo := &mockBlockOracle{}
	mbo.err = blocks.ErrMinerHasNoATXInPreviousEpoch
	builder.blockOracle = mbo
	require.NoError(t, builder.Start(context.TODO()))

	go func() { beginRound <- types.GetEffectiveGenesis().Add(1) }()
	select {
	case <-receiver:
		assert.Fail(t, "miner should not produce blocks")
	case <-time.After(500 * time.Millisecond):
	}

	ctrl.Finish()
}

func TestBlockBuilder_CreateBlockWithRef(t *testing.T) {
	hareRes := []types.BlockID{block1.ID(), block2.ID(), block3.ID(), block4.ID()}

	builder := createBlockBuilder(t, "a", newPublisher(t))
	builder.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{block4.ID()}, hareRes, {}}, nil
	}}

	recipient := types.BytesToAddress([]byte{0x01})
	signer := signing.NewEdSigner()

	trans := []*types.Transaction{
		NewTx(t, 1, recipient, signer),
		NewTx(t, 2, recipient, signer),
		NewTx(t, 3, recipient, signer),
	}

	beacon := types.HexToHash32("0x94812631").Bytes()
	transids := []types.TransactionID{trans[0].ID(), trans[1].ID(), trans[2].ID()}
	activeSet := []types.ATXID{atx1, atx2, atx3, atx4, atx5}
	b, err := builder.createBlock(context.TODO(), types.GetEffectiveGenesis().Add(1), types.ATXID(types.Hash32{1, 2, 3}), types.BlockEligibilityProof{J: 0, Sig: []byte{1}}, transids, activeSet, beacon)
	assert.NoError(t, err)

	assert.Equal(t, hareRes, b.ForDiff)
	assert.Equal(t, []types.BlockID{block4.ID()}, b.AgainstDiff)

	assert.True(t, ContainsTx(b.TxIDs, transids[0]))
	assert.True(t, ContainsTx(b.TxIDs, transids[1]))
	assert.True(t, ContainsTx(b.TxIDs, transids[2]))

	assert.Equal(t, activeSet, *b.ActiveSet)
	assert.Equal(t, beacon, b.TortoiseBeacon)

	// test create second block
	bl, err := builder.createBlock(context.TODO(), types.GetEffectiveGenesis().Add(2), types.ATXID(types.Hash32{1, 2, 3}), types.BlockEligibilityProof{J: 1, Sig: []byte{1}}, transids, activeSet, beacon)
	assert.NoError(t, err)

	assert.Equal(t, hareRes, bl.ForDiff)
	assert.Equal(t, []types.BlockID{block4.ID()}, bl.AgainstDiff)

	assert.True(t, ContainsTx(bl.TxIDs, transids[0]))
	assert.True(t, ContainsTx(bl.TxIDs, transids[1]))
	assert.True(t, ContainsTx(bl.TxIDs, transids[2]))

	assert.Equal(t, *bl.RefBlock, b.ID())
	assert.Nil(t, bl.ActiveSet)
	assert.Nil(t, bl.TortoiseBeacon)
}

func TestBlockBuilder_CreateBlockWithRef_FailedLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hareRes := []types.BlockID{block1.ID(), block2.ID(), block3.ID(), block4.ID()}

	builder := createBlockBuilder(t, "a", newPublisher(t))
	builder.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{block4.ID()}, hareRes, {}}, nil
	}}
	mockDB := dbMocks.NewMockDatabase(ctrl)
	builder.db = mockDB

	recipient := types.BytesToAddress([]byte{0x01})
	signer := signing.NewEdSigner()

	trans := []*types.Transaction{
		NewTx(t, 1, recipient, signer),
		NewTx(t, 2, recipient, signer),
		NewTx(t, 3, recipient, signer),
	}

	beacon := types.HexToHash32("0x94812631").Bytes()
	transids := []types.TransactionID{trans[0].ID(), trans[1].ID(), trans[2].ID()}
	activeSet := []types.ATXID{atx1, atx2, atx3, atx4, atx5}
	lyr := types.GetEffectiveGenesis().Add(1)
	dbErr := errors.New("unknown")
	mockDB.EXPECT().Get(getEpochKey(lyr.GetEpoch())).Return(nil, dbErr).Times(1)
	b, err := builder.createBlock(context.TODO(), lyr, types.ATXID(types.Hash32{1, 2, 3}), types.BlockEligibilityProof{J: 0, Sig: []byte{1}}, transids, activeSet, beacon)
	assert.ErrorIs(t, err, dbErr)
	assert.Nil(t, b)
}

func TestBlockBuilder_CreateBlockWithRef_FailedSave(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hareRes := []types.BlockID{block1.ID(), block2.ID(), block3.ID(), block4.ID()}

	builder := createBlockBuilder(t, "a", newPublisher(t))
	builder.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{block4.ID()}, hareRes, {}}, nil
	}}
	mockDB := dbMocks.NewMockDatabase(ctrl)
	builder.db = mockDB

	recipient := types.BytesToAddress([]byte{0x01})
	signer := signing.NewEdSigner()

	trans := []*types.Transaction{
		NewTx(t, 1, recipient, signer),
		NewTx(t, 2, recipient, signer),
		NewTx(t, 3, recipient, signer),
	}

	beacon := types.HexToHash32("0x94812631").Bytes()
	transids := []types.TransactionID{trans[0].ID(), trans[1].ID(), trans[2].ID()}
	activeSet := []types.ATXID{atx1, atx2, atx3, atx4, atx5}
	lyr := types.GetEffectiveGenesis().Add(1)
	dbErr := errors.New("unknown")
	mockDB.EXPECT().Get(getEpochKey(lyr.GetEpoch())).Return(nil, database.ErrNotFound).Times(1)
	mockDB.EXPECT().Put(getEpochKey(lyr.GetEpoch()), gomock.Any()).Return(dbErr).Times(1)
	b, err := builder.createBlock(context.TODO(), lyr, types.ATXID(types.Hash32{1, 2, 3}), types.BlockEligibilityProof{J: 0, Sig: []byte{1}}, transids, activeSet, beacon)
	assert.ErrorIs(t, err, dbErr)
	assert.Nil(t, b)
}

func NewTx(t *testing.T, nonce uint64, recipient types.Address, signer *signing.EdSigner) *types.Transaction {
	tx, err := types.NewSignedTx(nonce, recipient, 1, defaultGasLimit, defaultFee, signer)
	assert.NoError(t, err)
	return tx
}

func TestBlockBuilder_SerializeTrans(t *testing.T) {
	tx := NewTx(t, 1, types.BytesToAddress([]byte{0x02}), signing.NewEdSigner())
	buf, err := types.InterfaceToBytes(tx)
	assert.NoError(t, err)

	ntx, err := types.BytesToTransaction(buf)
	assert.NoError(t, err)
	err = ntx.CalcAndSetOrigin()
	assert.NoError(t, err)

	assert.Equal(t, *tx, *ntx)
}

func ContainsTx(a []types.TransactionID, x types.TransactionID) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

var (
	one   = types.CalcHash32([]byte("1"))
	two   = types.CalcHash32([]byte("2"))
	three = types.CalcHash32([]byte("3"))
	four  = types.CalcHash32([]byte("4"))
	five  = types.CalcHash32([]byte("5"))
)

var (
	atx1 = types.ATXID(one)
	atx2 = types.ATXID(two)
	atx3 = types.ATXID(three)
	atx4 = types.ATXID(four)
	atx5 = types.ATXID(five)
)

type mockMesh struct{}

func (m *mockMesh) AddBlockWithTxs(context.Context, *types.Block) error {
	return nil
}

func TestBlockBuilder_createBlock(t *testing.T) {
	r := require.New(t)
	types.SetLayersPerEpoch(3)
	block1 := types.NewExistingBlock(types.NewLayerID(6), []byte(rand.String(8)), nil)
	block2 := types.NewExistingBlock(types.NewLayerID(6), []byte(rand.String(8)), nil)
	block3 := types.NewExistingBlock(types.NewLayerID(6), []byte(rand.String(8)), nil)
	st := []types.BlockID{block1.ID(), block2.ID(), block3.ID()}
	builder1 := createBlockBuilder(t, "a", newPublisher(t))
	builder1.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{}, {}, st}, nil
	}}

	beacon := types.HexToHash32("0x94812631").Bytes()
	b, err := builder1.createBlock(context.TODO(), types.NewLayerID(7), types.ATXID{}, types.BlockEligibilityProof{}, nil, nil, beacon)
	r.Nil(err)
	r.Equal(st, b.NeutralDiff)

	builder1.baseBlockP = &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{0}, [][]types.BlockID{{}, nil, st}, nil
	}}

	b, err = builder1.createBlock(context.TODO(), types.NewLayerID(7), types.ATXID{}, types.BlockEligibilityProof{}, nil, nil, beacon)
	r.Nil(err)
	r.Equal([]types.BlockID(nil), b.ForDiff)
	emptyID := types.BlockID{}
	r.NotEqual(b.ID(), emptyID)

	_, err = builder1.createBlock(context.TODO(), types.NewLayerID(5), types.ATXID{}, types.BlockEligibilityProof{}, nil, nil, beacon)
	r.EqualError(err, "cannot create blockBytes in genesis layer")
}

func TestBlockBuilder_notSynced(t *testing.T) {
	r := require.New(t)
	beginRound := make(chan types.LayerID)
	ms := &mockSyncer{}
	ms.notSynced = true
	mbo := &mockBlockOracle{}

	builder := createBlockBuilder(t, "a", newPublisher(t))
	builder.syncer = ms
	builder.blockOracle = mbo
	builder.beginRoundEvent = beginRound
	require.NoError(t, builder.Start(context.TODO()))
	t.Cleanup(func() {
		builder.Close()
	})
	beginRound <- types.NewLayerID(1)
	beginRound <- types.NewLayerID(2)
	r.Equal(0, mbo.calls)
}

type mockBBP struct {
	f func() (types.BlockID, [][]types.BlockID, error)
}

func (b *mockBBP) BaseBlock(context.Context) (types.BlockID, [][]types.BlockID, error) {
	// XXX: for now try to not break all tests
	if b.f != nil {
		return b.f()
	}
	return types.BlockID{0}, [][]types.BlockID{{}, {}, {}}, nil
}

func createBlockBuilder(t *testing.T, ID string, publisher pubsub.Publisher) *BlockBuilder {
	beginRound := make(chan types.LayerID)
	cfg := Config{
		MinerID:        types.NodeID{Key: ID},
		AtxsPerBlock:   selectCount,
		LayersPerEpoch: 3,
		TxsPerBlock:    selectCount,
	}
	bb := NewBlockBuilder(cfg, signing.NewEdSigner(), publisher, beginRound, &mockMesh{}, &mockBBP{f: func() (types.BlockID, [][]types.BlockID, error) {
		return types.BlockID{}, [][]types.BlockID{{}, {}, {}}, nil
	}}, &mockBlockOracle{}, nil, &mockSyncer{}, mockProjector, nil, logtest.New(t).WithName(ID))
	return bb
}
