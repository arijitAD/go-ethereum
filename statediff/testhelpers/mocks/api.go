// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package mocks

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/statediff"
)

// MockStateDiffService is a mock state diff service
type MockStateDiffService struct {
	sync.Mutex
	Builder         statediff.Builder
	BlockChain      *BlockChain
	ReturnProtocol  []p2p.Protocol
	ReturnAPIs      []rpc.API
	BlockChan       chan *types.Block
	ParentBlockChan chan *types.Block
	QuitChan        chan bool
	Subscriptions   map[rpc.ID]statediff.Subscription
	streamBlock     bool
}

// Protocols mock method
func (sds *MockStateDiffService) Protocols() []p2p.Protocol {
	return []p2p.Protocol{}
}

// APIs mock method
func (sds *MockStateDiffService) APIs() []rpc.API {
	return []rpc.API{
		{
			Namespace: statediff.APIName,
			Version:   statediff.APIVersion,
			Service:   statediff.NewPublicStateDiffAPI(sds),
			Public:    true,
		},
	}
}

// Loop mock method
func (sds *MockStateDiffService) Loop(chan core.ChainEvent) {
	//loop through chain events until no more
	for {
		select {
		case block := <-sds.BlockChan:
			currentBlock := block
			parentBlock := <-sds.ParentBlockChan
			parentHash := parentBlock.Hash()
			if parentBlock == nil {
				log.Error("Parent block is nil, skipping this block",
					"parent block hash", parentHash.String(),
					"current block number", currentBlock.Number())
				continue
			}
			payload, err := sds.processStateDiff(currentBlock, parentBlock)
			if err != nil {
				log.Error("Error building statediff", "block number", currentBlock.Number(), "error", err)
				continue
			}
			sds.send(*payload)
		case <-sds.QuitChan:
			log.Debug("Quitting the statediff block channel")
			sds.close()
			return
		}
	}
}

// processStateDiff method builds the state diff payload from the current and parent block and streams it to listening subscriptions
func (sds *MockStateDiffService) processStateDiff(currentBlock, parentBlock *types.Block) (*statediff.Payload, error) {
	stateDiff, err := sds.Builder.BuildStateDiff(parentBlock.Root(), currentBlock.Root(), currentBlock.Number(), currentBlock.Hash())
	if err != nil {
		return nil, err
	}

	stateDiffRlp, err := rlp.EncodeToBytes(stateDiff)
	if err != nil {
		return nil, err
	}
	payload := statediff.Payload{
		StateDiffRlp: stateDiffRlp,
	}
	if sds.streamBlock {
		rlpBuff := new(bytes.Buffer)
		if err = currentBlock.EncodeRLP(rlpBuff); err != nil {
			return nil, err
		}
		payload.BlockRlp = rlpBuff.Bytes()
	}
	return &payload, nil
}

// Subscribe mock method
func (sds *MockStateDiffService) Subscribe(id rpc.ID, sub chan<- statediff.Payload, quitChan chan<- bool) {
	log.Info("Subscribing to the mock statediff service")
	sds.Lock()
	sds.Subscriptions[id] = statediff.Subscription{
		PayloadChan: sub,
		QuitChan:    quitChan,
	}
	sds.Unlock()
}

// Unsubscribe mock method
func (sds *MockStateDiffService) Unsubscribe(id rpc.ID) error {
	log.Info("Unsubscribing from the mock statediff service")
	sds.Lock()
	_, ok := sds.Subscriptions[id]
	if !ok {
		return fmt.Errorf("cannot unsubscribe; subscription for id %s does not exist", id)
	}
	delete(sds.Subscriptions, id)
	sds.Unlock()
	return nil
}

func (sds *MockStateDiffService) send(payload statediff.Payload) {
	sds.Lock()
	for id, sub := range sds.Subscriptions {
		select {
		case sub.PayloadChan <- payload:
			log.Info("sending state diff payload to subscription %s", id)
		default:
			log.Info("unable to send payload to subscription %s; channel has no receiver", id)
		}
	}
	sds.Unlock()
}

func (sds *MockStateDiffService) close() {
	sds.Lock()
	for id, sub := range sds.Subscriptions {
		select {
		case sub.QuitChan <- true:
			delete(sds.Subscriptions, id)
			log.Info("closing subscription %s", id)
		default:
			log.Info("unable to close subscription %s; channel has no receiver", id)
		}
	}
	sds.Unlock()
}

// Start mock method
func (sds *MockStateDiffService) Start(server *p2p.Server) error {
	log.Info("Starting mock statediff service")
	if sds.ParentBlockChan == nil || sds.BlockChan == nil {
		return errors.New("MockStateDiffingService needs to be configured with a MockParentBlockChan and MockBlockChan")
	}
	chainEventCh := make(chan core.ChainEvent, 10)
	go sds.Loop(chainEventCh)

	return nil
}

// Stop mock method
func (sds *MockStateDiffService) Stop() error {
	log.Info("Stopping mock statediff service")
	close(sds.QuitChan)
	return nil
}

// StateDiffAt mock method
func (sds *MockStateDiffService) StateDiffAt(blockNumber uint64) (*statediff.Payload, error) {
	currentBlock := sds.BlockChain.GetBlockByNumber(blockNumber)
	parentBlock := sds.BlockChain.GetBlockByHash(currentBlock.ParentHash())
	log.Info(fmt.Sprintf("sending state diff at %d", blockNumber))
	return sds.processStateDiff(currentBlock, parentBlock)
}
