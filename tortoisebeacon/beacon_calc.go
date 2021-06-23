package tortoisebeacon

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/events"
	"github.com/spacemeshos/go-spacemesh/log"
)

func (tb *TortoiseBeacon) calcBeacon(ctx context.Context, epoch types.EpochID) error {
	tb.Log.With().Info("Calculating beacon",
		log.Uint64("epoch_id", uint64(epoch)))

	allHashes, err := tb.calcTortoiseBeaconHashList(epoch)
	if err != nil {
		return fmt.Errorf("calc tortoise beacon hash list: %w", err)
	}

	tb.Log.With().Debug("Going to calculate tortoise beacon from this hash list",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("hashes", strings.Join(allHashes, ", ")))

	beacon := allHashes.Hash()

	tb.Log.With().Info("Calculated beacon",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("beacon", beacon.String()))

	tb.beaconsMu.Lock()
	tb.beacons[epoch] = beacon
	tb.beaconsMu.Unlock()

	tb.Log.With().Debug("Beacon updated for this epoch",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("beacon", beacon.String()))

	events.ReportCalculatedTortoiseBeacon(epoch, beacon.String())

	if err := tb.syncBeacon(ctx, epoch, beacon); err != nil {
		return fmt.Errorf("sync beacon: %w", err)
	}

	return nil
}

func (tb *TortoiseBeacon) syncBeacon(ctx context.Context, epoch types.EpochID, beacon types.Hash32) error {
	mb := BeaconSyncMessageBody{
		MinerID: tb.minerID,
		EpochID: epoch,
		Beacon:  beacon,
	}

	sig, err := tb.calcEligibilityProof(mb)
	if err != nil {
		return fmt.Errorf("calcEligibilityProof: %w", err)
	}

	m := BeaconSyncMessage{
		BeaconSyncMessageBody: mb,
		Signature:             sig,
	}

	tb.Log.With().Debug("Going to send beacon sync message",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("message", m.String()))

	if err := tb.sendToGossip(ctx, TBBeaconSyncProtocol, m); err != nil {
		return fmt.Errorf("sendToGossip: %w", err)
	}

	return nil
}

func (tb *TortoiseBeacon) syncPrevBeacon(ctx context.Context, epoch types.EpochID, beacon types.Hash32) error {
	mb := BeaconSyncMessageBody{
		MinerID: tb.minerID,
		EpochID: epoch,
		Beacon:  beacon,
	}

	sig, err := tb.calcEligibilityProof(mb)
	if err != nil {
		return fmt.Errorf("calcEligibilityProof: %w", err)
	}

	m := BeaconSyncMessage{
		BeaconSyncMessageBody: mb,
		Signature:             sig,
	}

	tb.Log.With().Debug("Going to send beacon prev sync message",
		log.Uint64("epoch_id", uint64(epoch)),
		log.String("message", m.String()))

	if err := tb.sendToGossip(ctx, TBBeaconSyncPrevProtocol, m); err != nil {
		return fmt.Errorf("sendToGossip: %w", err)
	}

	return nil
}

func (tb *TortoiseBeacon) calcTortoiseBeaconHashList(epoch types.EpochID) (proposalList, error) {
	allHashes := make(proposalList, 0)

	lastRound := epochRoundPair{
		EpochID: epoch,
		Round:   tb.lastPossibleRound(),
	}

	votes, ok := tb.ownVotes[lastRound]
	if !ok {
		// re-calculate votes
		tb.Log.With().Debug("Own votes not found, re-calculating",
			log.Uint64("epoch_id", uint64(epoch)),
			log.Uint64("round", uint64(lastRound.Round)))

		v, err := tb.calcVotes(epoch, lastRound.Round)
		if err != nil {
			return nil, fmt.Errorf("recalculate votes: %w", err)
		}

		votes = v
		tb.ownVotes[lastRound] = v
	}

	// output from VRF
	for vote := range votes.ValidVotes {
		allHashes = append(allHashes, vote)
	}

	tb.Log.With().Debug("Tortoise beacon last round votes",
		log.Uint64("epoch_id", uint64(epoch)),
		log.Uint64("round", uint64(lastRound.Round)),
		log.String("votes", fmt.Sprint(votes)))

	sort.Slice(allHashes, func(i, j int) bool {
		return strings.Compare(allHashes[i], allHashes[j]) == -1
	})

	return allHashes, nil
}
