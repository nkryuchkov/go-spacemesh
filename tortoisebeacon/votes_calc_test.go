package tortoisebeacon

import (
	"testing"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/p2p/p2pcrypto"
	"github.com/spacemeshos/go-spacemesh/tortoisebeacon/weakcoin"
	"github.com/stretchr/testify/require"
)

func TestTortoiseBeacon_calcVotesFromProposals(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	const epoch = 1

	tt := []struct {
		name                      string
		epoch                     types.EpochID
		validProposals            proposalsMap
		potentiallyValidProposals proposalsMap
		votesFor                  proposalList
		votesAgainst              proposalList
	}{
		{
			name:  "Case 1",
			epoch: epoch,
			validProposals: proposalsMap{
				epoch: hashSet{
					"0x1": {},
					"0x2": {},
					"0x3": {},
				},
			},
			potentiallyValidProposals: proposalsMap{
				epoch: hashSet{
					"0x4": {},
					"0x5": {},
					"0x6": {},
				},
			},
			votesFor: proposalList{
				"0x1",
				"0x2",
				"0x3",
			},
			votesAgainst: proposalList{
				"0x4",
				"0x5",
				"0x6",
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				Log:                       log.NewDefault("TortoiseBeacon"),
				validProposals:            tc.validProposals,
				potentiallyValidProposals: tc.potentiallyValidProposals,
			}

			frv := tb.calcVotesFromProposals(tc.epoch)
			r.EqualValues(tc.votesFor.Sort(), frv.ValidVotes)
			r.EqualValues(tc.votesAgainst.Sort(), frv.PotentiallyValidVotes)
		})
	}
}

func TestTortoiseBeacon_calcVotesDelta(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	_, pk1, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	_, pk2, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	const epoch = 5
	const round = 3

	tt := []struct {
		name          string
		epoch         types.EpochID
		round         types.RoundID
		incomingVotes map[epochRoundPair]votesPerPK
		forDiff       proposalList
		againstDiff   proposalList
	}{
		{
			name:  "Case 1",
			epoch: epoch,
			round: round,
			incomingVotes: map[epochRoundPair]votesPerPK{
				epochRoundPair{EpochID: epoch, Round: 1}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x2": {},
						},
						InvalidVotes: hashSet{
							"0x3": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x4": {},
							"0x5": {},
						},
						InvalidVotes: hashSet{
							"0x6": {},
						},
					},
				},
				epochRoundPair{EpochID: epoch, Round: 2}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x3": {},
						},
						InvalidVotes: hashSet{
							"0x2": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes:   hashSet{},
						InvalidVotes: hashSet{},
					},
				},
				epochRoundPair{EpochID: epoch, Round: 3}: {
					pk1.String(): votesSetPair{
						ValidVotes:   hashSet{},
						InvalidVotes: hashSet{},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x6": {},
						},
						InvalidVotes: hashSet{
							"0x5": {},
						},
					},
				},
			},
			forDiff:     proposalList{},
			againstDiff: proposalList{},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				Log:           log.NewDefault("TortoiseBeacon"),
				incomingVotes: tc.incomingVotes,
				ownVotes:      map[epochRoundPair]votesSetPair{},
			}

			forDiff, againstDiff := tb.calcVotes(tc.epoch, tc.round)
			r.EqualValues(tc.forDiff.Sort(), forDiff)
			r.EqualValues(tc.againstDiff.Sort(), againstDiff)
		})
	}
}

func TestTortoiseBeacon_firstRoundVotes(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	_, pk1, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	_, pk2, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	const epoch = 5
	const round = 3

	tt := []struct {
		name          string
		epoch         types.EpochID
		upToRound     types.RoundID
		incomingVotes map[epochRoundPair]votesPerPK
		votesCount    votesMarginMap
	}{
		{
			name:      "Case 1",
			epoch:     epoch,
			upToRound: round,
			incomingVotes: map[epochRoundPair]votesPerPK{
				epochRoundPair{EpochID: epoch, Round: 1}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x2": {},
						},
						InvalidVotes: hashSet{
							"0x3": {},
							"0x5": {},
							"0x6": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x4": {},
							"0x5": {},
						},
						InvalidVotes: hashSet{
							"0x6": {},
						},
					},
				},
			},
			votesCount: votesMarginMap{
				"0x1": 2,
				"0x2": 1,
				"0x3": -1,
				"0x4": 1,
				"0x5": 0,
				"0x6": -2,
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				Log:           log.NewDefault("TortoiseBeacon"),
				incomingVotes: tc.incomingVotes,
			}

			votesMargin, err := tb.firstRoundVotes(tc.epoch)
			r.NoError(err)
			r.EqualValues(tc.votesCount, votesMargin)
		})
	}
}

func TestTortoiseBeacon_calcOwnFirstRoundVotes(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	_, pk1, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	_, pk2, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	const epoch = 5
	const round = 3
	const threshold = 2

	tt := []struct {
		name          string
		epoch         types.EpochID
		upToRound     types.RoundID
		incomingVotes map[epochRoundPair]votesPerPK
		weakCoin      weakcoin.WeakCoin
		result        votesSetPair
	}{
		{
			name:      "Weak Coin is false",
			epoch:     epoch,
			upToRound: round,
			incomingVotes: map[epochRoundPair]votesPerPK{
				epochRoundPair{EpochID: epoch, Round: 1}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x2": {},
						},
						InvalidVotes: hashSet{
							"0x3": {},
							"0x6": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x4": {},
							"0x5": {},
						},
						InvalidVotes: hashSet{
							"0x6": {},
						},
					},
				},
			},
			weakCoin: weakcoin.ValueMock{Value: false},
			result: votesSetPair{
				ValidVotes: hashSet{
					"0x1": {},
				},
				InvalidVotes: hashSet{
					"0x2": {},
					"0x3": {},
					"0x4": {},
					"0x5": {},
					"0x6": {},
				},
			},
		},
		{
			name:      "Weak Coin is true",
			epoch:     epoch,
			upToRound: round,
			incomingVotes: map[epochRoundPair]votesPerPK{
				epochRoundPair{EpochID: epoch, Round: 1}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x2": {},
						},
						InvalidVotes: hashSet{
							"0x3": {},
							"0x6": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x4": {},
							"0x5": {},
						},
						InvalidVotes: hashSet{
							"0x6": {},
						},
					},
				},
			},
			weakCoin: weakcoin.ValueMock{Value: true},
			result: votesSetPair{
				ValidVotes: hashSet{
					"0x1": {},
					"0x2": {},
					"0x3": {},
					"0x4": {},
					"0x5": {},
				},
				InvalidVotes: hashSet{
					"0x6": {},
				},
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				config: Config{
					Theta: 1,
				},
				Log:           log.NewDefault("TortoiseBeacon"),
				weakCoin:      tc.weakCoin,
				incomingVotes: tc.incomingVotes,
				ownVotes:      map[epochRoundPair]votesSetPair{},
			}

			votesMargin, err := tb.firstRoundVotes(tc.epoch)
			r.NoError(err)

			result, err := tb.calcOwnFirstRoundVotes(tc.epoch, votesMargin)
			r.NoError(err)
			r.EqualValues(tc.result, result)
		})
	}
}

func TestTortoiseBeacon_calcVotesMargin(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	_, pk1, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	_, pk2, err := p2pcrypto.GenerateKeyPair()
	r.NoError(err)

	const epoch = 5
	const round = 3

	tt := []struct {
		name          string
		epoch         types.EpochID
		upToRound     types.RoundID
		incomingVotes map[epochRoundPair]votesPerPK
		result        votesMarginMap
	}{
		{
			name:      "Case 1",
			epoch:     epoch,
			upToRound: round,
			incomingVotes: map[epochRoundPair]votesPerPK{
				epochRoundPair{EpochID: epoch, Round: 1}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x2": {},
						},
						InvalidVotes: hashSet{
							"0x3": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x1": {},
							"0x4": {},
							"0x5": {},
						},
						InvalidVotes: hashSet{
							"0x6": {},
						},
					},
				},
				epochRoundPair{EpochID: epoch, Round: 2}: {
					pk1.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x3": {},
						},
						InvalidVotes: hashSet{
							"0x2": {},
						},
					},
					pk2.String(): votesSetPair{
						ValidVotes:   hashSet{},
						InvalidVotes: hashSet{},
					},
				},
				epochRoundPair{EpochID: epoch, Round: 3}: {
					pk1.String(): votesSetPair{
						ValidVotes:   hashSet{},
						InvalidVotes: hashSet{},
					},
					pk2.String(): votesSetPair{
						ValidVotes: hashSet{
							"0x6": {},
						},
						InvalidVotes: hashSet{
							"0x5": {},
						},
					},
				},
			},
			result: votesMarginMap{
				"0x1": 6,
				"0x2": 1,
				"0x3": -1,
				"0x4": 3,
				"0x5": 1,
				"0x6": -1,
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				Log:                      log.NewDefault("TortoiseBeacon"),
				incomingVotes:            tc.incomingVotes,
				firstRoundOutcomingVotes: map[types.EpochID]firstRoundVotes{},
			}

			votesMargin, err := tb.firstRoundVotes(tc.epoch)
			r.NoError(err)

			err = tb.calcVotesMargin(tc.epoch, tc.upToRound, votesMargin)
			r.NoError(err)
			r.EqualValues(tc.result, votesMargin)
		})
	}
}

func TestTortoiseBeacon_calcOwnCurrentRoundVotes(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	const threshold = 3

	tt := []struct {
		name               string
		epoch              types.EpochID
		round              types.RoundID
		ownFirstRoundVotes votesSetPair
		votesCount         votesMarginMap
		weakCoin           weakcoin.WeakCoin
		result             votesSetPair
	}{
		{
			name:  "Case 1",
			epoch: 5,
			round: 5,
			ownFirstRoundVotes: votesSetPair{
				ValidVotes: hashSet{
					"0x1": {},
					"0x2": {},
				},
				InvalidVotes: hashSet{
					"0x3": {},
				},
			},
			votesCount: votesMarginMap{
				"0x1": threshold * 2,
				"0x2": -threshold * 3,
				"0x3": threshold / 2,
			},
			weakCoin: weakcoin.ValueMock{Value: true},
			result: votesSetPair{
				ValidVotes: hashSet{
					"0x1": {},
					"0x3": {},
				},
				InvalidVotes: hashSet{
					"0x2": {},
				},
			},
		},
		{
			name:  "Case 2",
			epoch: 5,
			round: 5,
			votesCount: votesMarginMap{
				"0x1": threshold * 2,
				"0x2": -threshold * 3,
				"0x3": threshold / 2,
			},
			weakCoin: weakcoin.ValueMock{Value: false},
			result: votesSetPair{
				ValidVotes: hashSet{
					"0x1": {},
				},
				InvalidVotes: hashSet{
					"0x2": {},
					"0x3": {},
				},
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := TortoiseBeacon{
				config: Config{
					Theta: 1,
				},
				Log:      log.NewDefault("TortoiseBeacon"),
				ownVotes: map[epochRoundPair]votesSetPair{},
				weakCoin: tc.weakCoin,
			}

			result, err := tb.calcOwnCurrentRoundVotes(tc.epoch, tc.round, tc.votesCount)
			r.NoError(err)
			r.EqualValues(tc.result, result)
		})
	}
}
