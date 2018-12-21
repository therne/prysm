package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/boltdb/bolt"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	b "github.com/prysmaticlabs/prysm/beacon-chain/core/blocks"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/types"

	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
)

func createBlock(enc []byte) (*pb.BeaconBlock, error) {
	protoBlock := &pb.BeaconBlock{}
	err := proto.Unmarshal(enc, protoBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal encoding: %v", err)
	}
	return protoBlock, nil
}

// GetBlock accepts a block hash and returns the corresponding block.
// Returns nil if the block does not exist.
func (db *BeaconDB) GetBlock(hash [32]byte) (*pb.BeaconBlock, error) {
	var block *pb.BeaconBlock
	err := db.view(func(tx *bolt.Tx) error {
		b := tx.Bucket(blockBucket)

		enc := b.Get(hash[:])
		if enc == nil {
			return nil
		}

		var err error
		block, err = createBlock(enc)
		return err
	})

	return block, err
}

// HasBlock accepts a block hash and returns true if the block does not exist.
func (db *BeaconDB) HasBlock(hash [32]byte) bool {
	hasBlock := false
	// #nosec G104
	_ = db.view(func(tx *bolt.Tx) error {
		b := tx.Bucket(blockBucket)

		hasBlock = b.Get(hash[:]) != nil

		return nil
	})

	return hasBlock
}

// SaveBlock accepts a block and writes it to disk.
func (db *BeaconDB) SaveBlock(block *pb.BeaconBlock) error {
	hash, err := b.Hash(block)
	if err != nil {
		return fmt.Errorf("failed to hash block: %v", err)
	}
	enc, err := proto.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to encode block: %v", err)
	}

	return db.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(blockBucket)

		return b.Put(hash[:], enc)
	})
}

// GetChainHead returns the head of the main chain.
func (db *BeaconDB) GetChainHead() (*pb.BeaconBlock, error) {
	var block *pb.BeaconBlock
	err := db.view(func(tx *bolt.Tx) error {
		chainInfo := tx.Bucket(chainInfoBucket)
		mainChain := tx.Bucket(mainChainBucket)
		blockBkt := tx.Bucket(blockBucket)

		height := chainInfo.Get(mainChainHeightKey)
		if height == nil {
			return errors.New("unable to determine chain height")
		}

		blockhash := mainChain.Get(height)
		if blockhash == nil {
			return fmt.Errorf("hash at the current height not found: %d", height)
		}

		enc := blockBkt.Get(blockhash)
		if enc == nil {
			return fmt.Errorf("block not found: %x", blockhash)
		}

		var err error
		block, err = createBlock(enc)
		return err
	})

	return block, err
}

// UpdateChainHead atomically updates the head of the chain as well as the corresponding state changes
// Including a new crystallized state is optional.
func (db *BeaconDB) UpdateChainHead(block *pb.BeaconBlock, beaconState *types.BeaconState) error {
	blockHash, err := b.Hash(block)
	if err != nil {
		return fmt.Errorf("unable to get the block hash: %v", err)
	}

	beaconStateEnc, err := beaconState.Marshal()
	if err != nil {
		return fmt.Errorf("unable to encode the beacon state: %v", err)
	}

	slotBinary := encodeSlotNumber(block.GetSlot())

	return db.update(func(tx *bolt.Tx) error {
		blockBucket := tx.Bucket(blockBucket)
		chainInfo := tx.Bucket(chainInfoBucket)
		mainChain := tx.Bucket(mainChainBucket)

		if blockBucket.Get(blockHash[:]) == nil {
			return fmt.Errorf("expected block %#x to have already been saved before updating head: %v", blockHash, err)
		}

		if err := mainChain.Put(slotBinary, blockHash[:]); err != nil {
			return fmt.Errorf("failed to include the block in the main chain bucket: %v", err)
		}

		if err := chainInfo.Put(mainChainHeightKey, slotBinary); err != nil {
			return fmt.Errorf("failed to record the block as the head of the main chain: %v", err)
		}

		if err := chainInfo.Put(stateLookupKey, beaconStateEnc); err != nil {
			return fmt.Errorf("failed to save beacon state as canonical: %v", err)
		}
		return nil
	})
}

// GetBlockBySlot accepts a slot number and returns the corresponding block in the main chain.
// Returns nil if a block was not recorded for the given slot.
func (db *BeaconDB) GetBlockBySlot(slot uint64) (*pb.BeaconBlock, error) {
	var block *pb.BeaconBlock
	slotEnc := encodeSlotNumber(slot)

	err := db.view(func(tx *bolt.Tx) error {
		mainChain := tx.Bucket(mainChainBucket)
		blockBkt := tx.Bucket(blockBucket)

		blockhash := mainChain.Get(slotEnc)
		if blockhash == nil {
			return nil
		}

		enc := blockBkt.Get(blockhash)
		if enc == nil {
			return fmt.Errorf("block not found: %x", blockhash)
		}

		var err error
		block, err = createBlock(enc)
		return err
	})

	return block, err
}

// GetGenesisTime returns the timestamp for the genesis block
func (db *BeaconDB) GetGenesisTime() (time.Time, error) {
	genesis, err := db.GetBlockBySlot(0)
	if err != nil {
		return time.Time{}, fmt.Errorf("could not get genesis block: %v", err)
	}
	if genesis == nil {
		return time.Time{}, fmt.Errorf("genesis block not found: %v", err)
	}

	genesisTime, err := ptypes.Timestamp(genesis.GetTimestamp())
	if err != nil {
		return time.Time{}, fmt.Errorf("could not get genesis timestamp: %v", err)
	}
	return genesisTime, nil
}
