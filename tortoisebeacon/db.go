package tortoisebeacon

import (
	"fmt"
	"sync"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/database"
	"github.com/spacemeshos/go-spacemesh/log"
)

// DB holds beacons for epochs.
type DB struct {
	sync.RWMutex
	store database.Database
	log   log.Log
}

// NewDB creates a Tortoise Beacon DB.
func NewDB(dbStore database.Database, log log.Log) *DB {
	db := &DB{
		store: dbStore,
		log:   log,
	}

	return db
}

// GetTortoiseBeacon gets a Tortoise Beacon value for an epoch.
func (db *DB) GetTortoiseBeacon(epochID types.EpochID) (types.Hash32, bool) {
	id, err := db.store.Get(epochID.ToBytes())
	if err != nil {
		return types.Hash32{}, false
	}

	return types.BytesToHash(id), true
}

// SetTortoiseBeacon sets a Tortoise Beacon value for an epoch.
func (db *DB) SetTortoiseBeacon(epochID types.EpochID, beacon types.Hash32) error {
	db.log.Debug("added tortoise beacon for epoch %v: %v", epochID, beacon.String())

	err := db.store.Put(epochID.ToBytes(), beacon.Bytes())
	if err != nil {
		return fmt.Errorf("failed to add tortoise beacon: %w", err)
	}

	return nil
}
