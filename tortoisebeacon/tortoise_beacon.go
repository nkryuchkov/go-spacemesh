package tortoisebeacon

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ALTree/bigfloat"
	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/common/util"
	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/signing"
	"github.com/spacemeshos/go-spacemesh/timesync"
	"github.com/spacemeshos/go-spacemesh/tortoisebeacon/weakcoin"
)

const (
	protoName       = "TORTOISE_BEACON_PROTOCOL"
	proposalPrefix  = "TBP"
	cleanupInterval = 30 * time.Second
	cleanupEpochs   = 1000
	firstRound      = types.RoundID(1)
)

var (
	genesisBeacon = types.HexToHash32("0x00")
)

// Tortoise Beacon errors.
var (
	ErrUnknownMessageType  = errors.New("unknown message type")
	ErrBeaconNotCalculated = errors.New("beacon is not calculated for this epoch")
	ErrEmptyProposalList   = errors.New("proposal list is empty")
	ErrTimeout             = errors.New("waited for tortoise beacon calculation too long")
)

type broadcaster interface {
	Broadcast(ctx context.Context, channel string, data []byte) error
}

type tortoiseBeaconDB interface {
	GetTortoiseBeacon(epochID types.EpochID) (types.Hash32, bool)
	SetTortoiseBeacon(epochID types.EpochID, beacon types.Hash32) error
}

type epochRoundPair struct {
	EpochID types.EpochID
	Round   types.RoundID
}

type (
	nodeID          = string
	proposal        = string
	hashSet         = map[proposal]struct{}
	firstRoundVotes = struct {
		ValidVotes            []proposal
		PotentiallyValidVotes []proposal
	}
	firstRoundVotesPerPK    = map[nodeID]firstRoundVotes
	votesPerPK              = map[nodeID]votesSetPair
	firstRoundVotesPerEpoch = map[types.EpochID]firstRoundVotesPerPK
	votesPerRound           = map[epochRoundPair]votesPerPK
	ownVotes                = map[epochRoundPair]votesSetPair
	votesMarginMap          = map[proposal]int
	proposalsMap            = map[types.EpochID]hashSet
)

// TortoiseBeacon represents Tortoise Beacon.
type TortoiseBeacon struct {
	util.Closer
	log.Log

	config        Config
	minerID       types.NodeID
	layerDuration time.Duration

	net              broadcaster
	atxDB            activationDB
	tortoiseBeaconDB tortoiseBeaconDB
	edSigner         *signing.EdSigner
	vrfVerifier      verifierFunc
	vrfSigner        signer
	weakCoin         weakcoin.WeakCoin

	layerMu   sync.RWMutex
	lastLayer types.LayerID

	clock                    layerClock
	layerTicker              chan types.LayerID
	q                        *big.Rat
	gracePeriodDuration      time.Duration
	proposalDuration         time.Duration
	firstVotingRoundDuration time.Duration
	votingRoundDuration      time.Duration
	weakCoinRoundDuration    time.Duration

	currentRoundsMu sync.RWMutex
	currentRounds   map[types.EpochID]types.RoundID

	validProposalsMu sync.RWMutex
	validProposals   proposalsMap

	potentiallyValidProposalsMu sync.RWMutex
	potentiallyValidProposals   proposalsMap

	votesMu                         sync.RWMutex
	firstRoundIncomingVotes         firstRoundVotesPerEpoch           // all rounds - votes (decoded votes)
	firstRoundOutcomingVotes        map[types.EpochID]firstRoundVotes // all rounds - votes (decoded votes)
	incomingVotes                   votesPerRound                     // all rounds - votes (decoded votes)
	ownVotes                        ownVotes                          // all rounds - own votes
	proposalPhaseFinishedTimestamps map[types.EpochID]time.Time

	beaconsMu sync.RWMutex
	beacons   map[types.EpochID]types.Hash32

	backgroundWG sync.WaitGroup
}

// a function to verify the message with the signature and its public key.
type verifierFunc = func(pub, msg, sig []byte) bool

type signer interface {
	Sign(msg []byte) []byte
}

type layerClock interface {
	Subscribe() timesync.LayerTimer
	Unsubscribe(timer timesync.LayerTimer)
	AwaitLayer(layerID types.LayerID) chan struct{}
	GetCurrentLayer() types.LayerID
	LayerToTime(id types.LayerID) time.Time
}

// New returns a new TortoiseBeacon.
func New(
	conf Config,
	minerID types.NodeID,
	layerDuration time.Duration,
	net broadcaster,
	atxDB activationDB,
	tortoiseBeaconDB tortoiseBeaconDB,
	edSigner *signing.EdSigner,
	vrfVerifier verifierFunc,
	vrfSigner signer,
	weakCoin weakcoin.WeakCoin,
	clock layerClock,
	logger log.Log,
) *TortoiseBeacon {
	q, ok := new(big.Rat).SetString(conf.Q)
	if !ok {
		panic("bad q parameter")
	}

	return &TortoiseBeacon{
		Log:                             logger,
		Closer:                          util.NewCloser(),
		config:                          conf,
		minerID:                         minerID,
		layerDuration:                   layerDuration,
		net:                             net,
		atxDB:                           atxDB,
		tortoiseBeaconDB:                tortoiseBeaconDB,
		edSigner:                        edSigner,
		vrfVerifier:                     vrfVerifier,
		vrfSigner:                       vrfSigner,
		weakCoin:                        weakCoin,
		clock:                           clock,
		q:                               q,
		gracePeriodDuration:             time.Duration(conf.GracePeriodDurationSec) * time.Second,
		proposalDuration:                time.Duration(conf.ProposalDurationSec) * time.Second,
		firstVotingRoundDuration:        time.Duration(conf.FirstVotingRoundDurationSec) * time.Second,
		votingRoundDuration:             time.Duration(conf.VotingRoundDurationSec) * time.Second,
		weakCoinRoundDuration:           time.Duration(conf.WeakCoinRoundDuration) * time.Second,
		currentRounds:                   make(map[types.EpochID]types.RoundID),
		validProposals:                  make(map[types.EpochID]hashSet),
		potentiallyValidProposals:       make(map[types.EpochID]hashSet),
		ownVotes:                        make(ownVotes),
		beacons:                         make(map[types.EpochID]types.Hash32),
		proposalPhaseFinishedTimestamps: map[types.EpochID]time.Time{},
		incomingVotes:                   map[epochRoundPair]votesPerPK{},
		firstRoundIncomingVotes:         map[types.EpochID]firstRoundVotesPerPK{},
		firstRoundOutcomingVotes:        map[types.EpochID]firstRoundVotes{},
	}
}

// Start starts listening for layers and outputs.
func (tb *TortoiseBeacon) Start(ctx context.Context) error {
	tb.Log.Info("Starting %v with the following config: %+v", protoName, tb.config)

	tb.initGenesisBeacons()

	tb.layerTicker = tb.clock.Subscribe()

	tb.backgroundWG.Add(1)

	go func() {
		defer tb.backgroundWG.Done()

		tb.listenLayers(ctx)
	}()

	tb.backgroundWG.Add(1)

	go func() {
		defer tb.backgroundWG.Done()

		tb.cleanupLoop()
	}()

	return nil
}

func (tb *TortoiseBeacon) initGenesisBeacons() {
	closedCh := make(chan struct{})
	close(closedCh)

	epoch := types.EpochID(0)
	for ; epoch.IsGenesis(); epoch++ {
		tb.beacons[epoch] = genesisBeacon
	}
}

// Close closes TortoiseBeacon.
func (tb *TortoiseBeacon) Close() error {
	tb.Log.Info("Closing %v", protoName)
	tb.Closer.Close()
	tb.backgroundWG.Wait() // Wait until background goroutines finish
	tb.clock.Unsubscribe(tb.layerTicker)

	return nil
}

// GetBeacon returns a Tortoise Beacon value as []byte for a certain epoch or an error if it doesn't exist.
func (tb *TortoiseBeacon) GetBeacon(epochID types.EpochID) ([]byte, error) {
	if tb.tortoiseBeaconDB != nil {
		if val, ok := tb.tortoiseBeaconDB.GetTortoiseBeacon(epochID); ok {
			return val.Bytes(), nil
		}
	}

	if (epochID - 1).IsGenesis() {
		return genesisBeacon.Bytes(), nil
	}

	tb.beaconsMu.RLock()
	defer tb.beaconsMu.RUnlock()

	var beacon types.Hash32
	var ok bool
	// TODO: remove
	for i := 0; i < 50; i++ {
		beacon, ok = tb.beacons[epochID-1]
		if !ok {
			tb.Log.Warning("beacon not calculated yet, waiting")
			time.Sleep(1 * time.Second)
			continue
			//return nil, ErrBeaconNotCalculated
		}
		break
	}
	tb.Log.Error("beacon not calculated after all attempts")

	if tb.tortoiseBeaconDB != nil {
		if err := tb.tortoiseBeaconDB.SetTortoiseBeacon(epochID, beacon); err != nil {
			return nil, fmt.Errorf("update beacon in DB: %w", err)
		}
	}

	return beacon.Bytes(), nil
}

func (tb *TortoiseBeacon) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-tb.CloseChannel():
			return
		case <-ticker.C:
			tb.cleanup()
		}
	}
}

func (tb *TortoiseBeacon) cleanup() {
	// TODO(nkryuchkov): implement a better solution, consider https://github.com/golang/go/issues/20135
	tb.beaconsMu.Lock()
	defer tb.beaconsMu.Unlock()

	for e := range tb.beacons {
		if tb.epochIsOutdated(e) {
			delete(tb.beacons, e)
		}
	}
}

func (tb *TortoiseBeacon) epochIsOutdated(epoch types.EpochID) bool {
	return tb.currentEpoch()-epoch > cleanupEpochs
}

// listens to new layers.
func (tb *TortoiseBeacon) listenLayers(ctx context.Context) {
	for {
		select {
		case <-tb.CloseChannel():
			return
		case layer := <-tb.layerTicker:
			tb.Log.With().Info("Received tick",
				log.Uint64("layer", uint64(layer)))

			go tb.handleLayer(ctx, layer)
		}
	}
}

// the logic that happens when a new layer arrives.
// this function triggers the start of new CPs.
func (tb *TortoiseBeacon) handleLayer(ctx context.Context, layer types.LayerID) {
	tb.layerMu.Lock()
	if layer > tb.lastLayer {
		tb.lastLayer = layer
	}

	tb.layerMu.Unlock()

	epoch := layer.GetEpoch()

	if !layer.FirstInEpoch() {
		tb.Log.With().Info("skipping layer because it's not first in this epoch",
			log.Uint64("epoch_id", uint64(epoch)),
			log.Uint64("layer_id", uint64(layer)))

		return
	}

	tb.Log.With().Info("tortoise beacon got tick",
		log.Uint64("layer", uint64(layer)),
		log.Uint64("epoch_id", uint64(epoch)))

	tb.handleEpoch(ctx, epoch)
}

func (tb *TortoiseBeacon) handleEpoch(ctx context.Context, epoch types.EpochID) {
	// TODO: check when epoch started, adjust waiting time for this timestamp

	if epoch.IsGenesis() {
		tb.Log.With().Info("not starting tortoise beacon since we are in genesis epoch",
			log.Uint64("epoch_id", uint64(epoch)))

		return
	}

	tb.Log.With().Info("Handling epoch",
		log.Uint64("epoch_id", uint64(epoch)))

	if err := tb.runProposalPhase(ctx, epoch); err != nil {
		tb.Log.With().Error("Failed to send proposal",
			log.Uint64("epoch_id", uint64(epoch)),
			log.Err(err))

		return
	}

	if err := tb.runConsensusPhase(ctx, epoch); err != nil {
		tb.Log.With().Error("Failed to run consensus phase",
			log.Uint64("epoch_id", uint64(epoch)),
			log.Err(err))
	}

	// K rounds passed
	// After K rounds had passed, tally up votes for proposals using simple tortoise vote counting
	if err := tb.calcBeacon(epoch); err != nil {
		tb.Log.With().Error("Failed to calculate beacon",
			log.Uint64("epoch_id", uint64(epoch)),
			log.Err(err))
	}
}

func (tb *TortoiseBeacon) runProposalPhase(ctx context.Context, epoch types.EpochID) error {
	proposedSignature, err := tb.calcProposalSignature(epoch)
	if err != nil {
		return fmt.Errorf("calculate signed proposal: %w", err)
	}

	tb.Log.With().Info("Calculated proposal signature",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("signature", util.Bytes2Hex(proposedSignature)))

	epochWeight, _, err := tb.atxDB.GetEpochWeight(epoch)
	if err != nil {
		return fmt.Errorf("get epoch weight: %w", err)
	}

	passes, err := tb.proposalPassesEligibilityThreshold(proposedSignature, epochWeight)
	if err != nil {
		return fmt.Errorf("proposalPassesEligibilityThreshold: %w", err)
	}

	if !passes {
		tb.Log.With().Info("Proposal doesn't pass threshold",
			log.Uint64("epoch_id", uint64(epoch)),
			log.String("proposal", util.Bytes2Hex(proposedSignature)),
			log.Uint64("weight", epochWeight))
		// proposal is not sent
		return nil
	}

	tb.Log.With().Info("Proposal passes threshold",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("proposal", util.Bytes2Hex(proposedSignature)),
		log.Uint64("weight", epochWeight))

	// concat them into a single proposal message
	m := ProposalMessage{
		MinerID:      tb.minerID,
		VRFSignature: proposedSignature,
	}

	tb.Log.With().Info("Going to send proposal",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("message", m.String()))

	if err := tb.sendToGossip(ctx, TBProposalProtocol, m); err != nil {
		return fmt.Errorf("broadcast proposal message: %w", err)
	}

	tb.validProposalsMu.Lock()

	if _, ok := tb.validProposals[epoch]; !ok {
		tb.validProposals[epoch] = make(map[proposal]struct{})
	}

	tb.validProposals[epoch][util.Bytes2Hex(proposedSignature)] = struct{}{}

	tb.validProposalsMu.Unlock()

	return nil
}

func (tb *TortoiseBeacon) proposalPassesEligibilityThreshold(proposal []byte, epochWeight uint64) (bool, error) {
	proposalInt := new(big.Int).SetBytes(proposal[:])

	threshold, err := tb.atxThreshold(epochWeight)
	if err != nil {
		return false, fmt.Errorf("atxThreshold: %w", err)
	}

	tb.Log.With().Info("Checking proposal for ATX threshold",
		log.String("proposal", proposalInt.String()),
		log.String("threshold", threshold.String()))

	return proposalInt.Cmp(threshold) == -1, nil
}

func (tb *TortoiseBeacon) runConsensusPhase(ctx context.Context, epoch types.EpochID) error {
	// rounds 1 to K
	ticker := time.NewTicker(tb.votingRoundDuration + tb.weakCoinRoundDuration)
	defer ticker.Stop()

	go func() {
		if err := tb.sendVotes(ctx, epoch, firstRound); err != nil {
			tb.Log.With().Error("Failed to send first voting message",
				log.Uint64("epoch_id", uint64(epoch)),
				log.Err(err))
		}
	}()

	// For K rounds: In each round that lasts δ, wait for proposals to come in.
	// For next rounds,
	// wait for δ time, and construct a message that points to all messages from previous round received by δ.
	for round := firstRound + 1; round <= tb.lastPossibleRound(); round++ {
		select {
		case <-ticker.C:
			go func(epoch types.EpochID, round types.RoundID) {
				if round > firstRound+1 {
					tb.weakCoin.OnRoundFinished(epoch, round-1)
				}

				if err := tb.sendVotes(ctx, epoch, round); err != nil {
					tb.Log.With().Error("Failed to send voting messages",
						log.Uint64("epoch_id", uint64(epoch)),
						log.Uint64("round", uint64(round)),
						log.Err(err))
				}
			}(epoch, round)

			go func(epoch types.EpochID, round types.RoundID) {
				t := time.NewTimer(tb.votingRoundDuration)
				defer t.Stop()

				select {
				case <-t.C:
					break
				case <-tb.CloseChannel():
					return
				case <-ctx.Done():
					return
				}

				tb.weakCoin.OnRoundStarted(epoch, round)

				// TODO(nkryuchkov):
				// should be published only after we should have received them
				if err := tb.weakCoin.PublishProposal(ctx, epoch, round); err != nil {
					tb.Log.With().Error("Failed to publish weak coin proposal",
						log.Uint64("epoch_id", uint64(epoch)),
						log.Uint64("round", uint64(round)),
						log.Err(err))
				}
			}(epoch, round)
		case <-tb.CloseChannel():
			return nil

		case <-ctx.Done():
			return nil
		}
	}

	tb.waitAfterLastRoundStarted()
	tb.weakCoin.OnRoundFinished(epoch, tb.lastPossibleRound())

	return nil
}

func (tb *TortoiseBeacon) sendVotes(ctx context.Context, epoch types.EpochID, round types.RoundID) error {
	tb.setCurrentRound(epoch, round)

	if round == firstRound {
		return tb.sendProposalVote(ctx, epoch)
	}

	return tb.sendVotesDifference(ctx, epoch, round)
}

func (tb *TortoiseBeacon) sendProposalVote(ctx context.Context, epoch types.EpochID) error {
	// round 1, send hashed proposal
	// create a voting message that references all seen proposals within δ time frame and send it
	votes := tb.calcVotesFromProposals(epoch)
	return tb.sendFirstRoundVote(ctx, epoch, votes)
}

func (tb *TortoiseBeacon) sendVotesDifference(ctx context.Context, epoch types.EpochID, round types.RoundID) error {
	// next rounds, send vote
	// construct a message that points to all messages from previous round received by δ
	ownCurrentRoundVotes, err := tb.calcVotes(epoch, round)
	if err != nil {
		return fmt.Errorf("calculate votes: %w", err)
	}

	return tb.sendFollowingVote(ctx, epoch, round, ownCurrentRoundVotes)
}

func (tb *TortoiseBeacon) sendFirstRoundVote(ctx context.Context, epoch types.EpochID, vote firstRoundVotes) error {
	valid := make([][]byte, 0)
	potentiallyValid := make([][]byte, 0)

	for _, v := range vote.ValidVotes {
		valid = append(valid, util.Hex2Bytes(v))
	}

	for _, v := range vote.PotentiallyValidVotes {
		potentiallyValid = append(potentiallyValid, util.Hex2Bytes(v))
	}

	mb := FirstVotingMessageBody{
		MinerID:                   tb.minerID,
		ValidProposals:            valid,
		PotentiallyValidProposals: potentiallyValid,
	}

	sig, err := tb.calcEligibilityProof(mb)
	if err != nil {
		return fmt.Errorf("calcEligibilityProof: %w", err)
	}

	m := FirstVotingMessage{
		FirstVotingMessageBody: mb,
		Signature:              sig,
	}

	tb.Log.With().Info("Going to send first round vote",
		log.Uint64("epoch_id", uint64(epoch)),
		log.Uint64("round", uint64(1)),
		log.String("message", m.String()))

	if err := tb.sendToGossip(ctx, TBFirstVotingProtocol, m); err != nil {
		return fmt.Errorf("sendToGossip: %w", err)
	}

	return nil
}

func (tb *TortoiseBeacon) sendFollowingVote(ctx context.Context, epoch types.EpochID, round types.RoundID, ownCurrentRoundVotes votesSetPair) error {
	bitVector := tb.encodeVotes(ownCurrentRoundVotes, tb.firstRoundOutcomingVotes[epoch])

	mb := FollowingVotingMessageBody{
		MinerID:        tb.minerID,
		EpochID:        epoch,
		RoundID:        round,
		VotesBitVector: bitVector,
	}

	sig, err := tb.calcEligibilityProof(mb)
	if err != nil {
		return fmt.Errorf("calcProposalSignature: %w", err)
	}

	m := FollowingVotingMessage{
		FollowingVotingMessageBody: mb,
		Signature:                  sig,
	}

	tb.Log.With().Info("Going to send following round vote",
		log.Uint64("epoch_id", uint64(epoch)),
		log.Uint64("round", uint64(round)),
		log.String("message", m.String()))

	if err := tb.sendToGossip(ctx, TBFollowingVotingProtocol, m); err != nil {
		return fmt.Errorf("broadcast voting message: %w", err)
	}

	return nil
}

func (tb *TortoiseBeacon) setCurrentRound(epoch types.EpochID, round types.RoundID) {
	tb.currentRoundsMu.Lock()
	defer tb.currentRoundsMu.Unlock()

	tb.currentRounds[epoch] = round
}

func (tb *TortoiseBeacon) voteWeight(pk nodeID, epochID types.EpochID) (uint64, error) {
	// TODO(nkryuchkov): enable
	enabled := false
	if !enabled {
		return 1, nil
	}

	nodeID := types.NodeID{
		Key:          pk,
		VRFPublicKey: nil,
	}

	atxID, err := tb.atxDB.GetNodeAtxIDForEpoch(nodeID, epochID)
	if err != nil {
		return 0, fmt.Errorf("atx ID for epoch: %w", err)
	}

	atx, err := tb.atxDB.GetAtxHeader(atxID)
	if err != nil {
		return 0, fmt.Errorf("atx header: %w", err)
	}

	return atx.GetWeight(), nil
}

// Each smesher partitions the valid proposals received in the previous epoch into three sets:
// - Timely proposals: received up to δ after the end of the previous epoch.
// - Delayed proposals: received between δ and 2δ after the end of the previous epoch.
// - Late proposals: more than 2δ after the end of the previous epoch.
// Note that honest users cannot disagree on timing by more than δ,
// so if a proposal is timely for any honest user,
// it cannot be late for any honest user (and vice versa).
func (tb *TortoiseBeacon) lastPossibleRound() types.RoundID {
	return types.RoundID(tb.config.RoundsNumber)
}

func (tb *TortoiseBeacon) waitAfterLastRoundStarted() {
	// Last round + next round for timely messages + next round for delayed messages (late messages may be ignored).
	const roundsToWait = 3
	timeToWait := roundsToWait * (tb.votingRoundDuration + tb.weakCoinRoundDuration)
	timer := time.NewTimer(timeToWait)

	select {
	case <-tb.CloseChannel():
	case <-timer.C:
	}
}

func (tb *TortoiseBeacon) votingThreshold(epochID types.EpochID) (int, error) {
	epochWeight, _, err := tb.atxDB.GetEpochWeight(epochID)
	if err != nil {
		return 0, fmt.Errorf("get epoch weight: %w", err)
	}

	return int(tb.config.Theta * float64(epochWeight)), nil
}

func (tb *TortoiseBeacon) atxThresholdFraction(epochWeight uint64) *big.Float {
	// threshold(k, q, W) = 1 - (2 ^ (- (k/((1-q)*W))
	// Floating point: 1 - math.Pow(2.0, -(float64(tb.config.Kappa)/((1.0-tb.config.Q)*float64(epochWeight))))
	// Fixed point:
	return new(big.Float).Sub(
		new(big.Float).SetInt64(1),
		bigfloat.Pow(
			new(big.Float).SetInt64(2),
			new(big.Float).SetRat(
				new(big.Rat).Neg(
					new(big.Rat).Quo(
						new(big.Rat).SetUint64(tb.config.Kappa),
						new(big.Rat).Mul(
							new(big.Rat).Sub(
								new(big.Rat).SetInt64(1.0),
								tb.q,
							),
							new(big.Rat).SetUint64(epochWeight),
						),
					),
				),
			),
		),
	)
}

// TODO: consider having a generic function for probabilities
func (tb *TortoiseBeacon) atxThreshold(epochWeight uint64) (*big.Int, error) {
	const signatureLength = 64 * 8

	fraction := tb.atxThresholdFraction(epochWeight)

	two := big.NewInt(2)
	signatureLengthBigInt := big.NewInt(signatureLength)

	maxPossibleNumberBigInt := new(big.Int).Exp(two, signatureLengthBigInt, nil)
	maxPossibleNumberBigFloat := new(big.Float).SetInt(maxPossibleNumberBigInt)

	thresholdBigFloat := new(big.Float).Mul(maxPossibleNumberBigFloat, fraction)
	threshold, _ := thresholdBigFloat.Int(nil)

	return threshold, nil
}

func (tb *TortoiseBeacon) calcProposalSignature(epoch types.EpochID) ([]byte, error) {
	p, err := tb.calcProposal(epoch)
	if err != nil {
		return nil, fmt.Errorf("calculate proposal: %w", err)
	}

	signature := tb.vrfSigner.Sign(p)
	tb.Log.With().Info("Calculated signature",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("proposal", util.Bytes2Hex(p)),
		log.String("signature", util.Bytes2Hex(signature)))

	return signature, nil
}

func (tb *TortoiseBeacon) calcEligibilityProof(message interface{}) ([]byte, error) {
	encoded, err := types.InterfaceToBytes(message)
	if err != nil {
		return nil, err
	}

	return tb.edSigner.Sign(encoded), nil
}

func (tb *TortoiseBeacon) calcProposal(epoch types.EpochID) ([]byte, error) {
	message := &struct {
		Prefix string
		Epoch  uint64
	}{
		Prefix: proposalPrefix,
		Epoch:  uint64(epoch),
	}

	return types.InterfaceToBytes(message)
}

func ceilDuration(duration, multiple time.Duration) time.Duration {
	result := duration.Truncate(multiple)
	if duration%multiple != 0 {
		result += multiple
	}

	return result
}

func (tb *TortoiseBeacon) sendToGossip(ctx context.Context, channel string, data interface{}) error {
	serialized, err := types.InterfaceToBytes(data)
	if err != nil {
		return fmt.Errorf("serializing: %w", err)
	}

	return tb.net.Broadcast(ctx, channel, serialized)
}
