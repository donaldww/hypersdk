// Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	autils "github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"

	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/consts"
	"github.com/ava-labs/hypersdk/utils"
)

var (
	_ snowman.Block           = &StatelessBlock{}
	_ block.WithVerifyContext = &StatelessBlock{}
	_ block.StateSummary      = &SyncableBlock{}
)

type StatefulBlock struct {
	Parent    ids.ID `json:"parent"`
	Height    uint64 `json:"height"`
	Timestamp int64  `json:"timestamp"`

	// AvailableChunks is a collection of valid Chunks that will be executed in
	// the future.
	AvailableChunks []*ChunkCertificate `json:"availableChunks"`

	// StartRoot is the root of the post-execution state
	// of [Parent].
	//
	// This "deferred root" design allows for merklization
	// to be done asynchronously instead of during [Build]
	// or [Verify], which reduces the amount of time we are
	// blocking the consensus engine from voting on the block,
	// starting the verification of another block, etc.
	StartRoot      ids.ID   `json:"startRoot"`
	ExecutedChunks []ids.ID `json:"executedChunks"`

	built bool
}

func (b *StatefulBlock) Size() int {
	return consts.IDLen + consts.Uint64Len + consts.Int64Len +
		consts.IntLen + codec.CummSize(b.AvailableChunks) +
		consts.IDLen + consts.IntLen + len(b.ExecutedChunks)*consts.IDLen
}

func (b *StatefulBlock) ID() (ids.ID, error) {
	blk, err := b.Marshal()
	if err != nil {
		return ids.ID{}, err
	}
	return utils.ToID(blk), nil
}

func (b *StatefulBlock) Marshal() ([]byte, error) {
	p := codec.NewWriter(b.Size(), consts.NetworkSizeLimit)

	p.PackID(b.Parent)
	p.PackUint64(b.Height)
	p.PackInt64(b.Timestamp)

	p.PackInt(len(b.AvailableChunks))
	for _, cert := range b.AvailableChunks {
		if err := cert.MarshalPacker(p); err != nil {
			return nil, err
		}
	}

	p.PackID(b.StartRoot)
	p.PackInt(len(b.ExecutedChunks))
	for _, chunk := range b.ExecutedChunks {
		p.PackID(chunk)
	}

	bytes := p.Bytes()
	if err := p.Err(); err != nil {
		return nil, err
	}
	return bytes, nil
}

func UnmarshalBlock(raw []byte) (*StatefulBlock, error) {
	var (
		p = codec.NewReader(raw, consts.NetworkSizeLimit)
		b StatefulBlock
	)

	p.UnpackID(false, &b.Parent)
	b.Height = p.UnpackUint64(false)
	b.Timestamp = p.UnpackInt64(false)

	// Parse available chunks
	chunkCount := p.UnpackInt(false)          // can produce empty blocks
	b.AvailableChunks = []*ChunkCertificate{} // don't preallocate all to avoid DoS
	for i := 0; i < chunkCount; i++ {
		cert, err := UnmarshalChunkCertificatePacker(p)
		if err != nil {
			return nil, err
		}
		b.AvailableChunks = append(b.AvailableChunks, cert)
	}

	// Parse executed chunks
	p.UnpackID(false, &b.StartRoot)
	chunkCount = p.UnpackInt(false) // can produce empty blocks
	b.ExecutedChunks = []ids.ID{}   // don't preallocate all to avoid DoS
	for i := 0; i < chunkCount; i++ {
		var id ids.ID
		p.UnpackID(true, &id)
		b.ExecutedChunks = append(b.ExecutedChunks, id)
	}

	// Ensure no leftover bytes
	if !p.Empty() {
		return nil, fmt.Errorf("%w: remaining=%d", ErrInvalidObject, len(raw)-p.Offset())
	}
	return &b, p.Err()
}

func NewGenesisBlock(root ids.ID) *StatefulBlock {
	return &StatefulBlock{
		// We set the genesis block timestamp to be after the ProposerVM fork activation.
		//
		// This prevents an issue (when using millisecond timestamps) during ProposerVM activation
		// where the child timestamp is rounded down to the nearest second (which may be before
		// the timestamp of its parent, which is denoted in milliseconds).
		//
		// Link: https://github.com/ava-labs/avalanchego/blob/0ec52a9c6e5b879e367688db01bb10174d70b212
		// .../vms/proposervm/pre_fork_block.go#L201
		Timestamp: time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),

		// StartRoot should include all allocates made when loading the genesis file
		StartRoot: root,
	}
}

// Stateless is defined separately from "Block"
// in case external packages needs use the stateful block
// without mocking VM or parent block
type StatelessBlock struct {
	*StatefulBlock `json:"block"`

	vm VM

	id    ids.ID
	st    choices.Status
	t     time.Time
	bytes []byte

	parent     *StatelessBlock
	execHeight *uint64

	chunks set.Set[ids.ID]

	bctx *block.Context
}

func NewBlock(vm VM, parent snowman.Block, tmstp int64) *StatelessBlock {
	return &StatelessBlock{
		StatefulBlock: &StatefulBlock{
			Parent:    parent.ID(),
			Timestamp: tmstp,
			Height:    parent.Height() + 1,
		},
		vm: vm,
		st: choices.Processing,
	}
}

func ParseBlock(
	ctx context.Context,
	source []byte,
	status choices.Status,
	vm VM,
) (*StatelessBlock, error) {
	ctx, span := vm.Tracer().Start(ctx, "chain.ParseBlock")
	defer span.End()

	blk, err := UnmarshalBlock(source)
	if err != nil {
		return nil, err
	}
	// Not guaranteed that a parsed block is verified
	return ParseStatefulBlock(ctx, blk, source, status, vm)
}

func ParseStatefulBlock(
	ctx context.Context,
	blk *StatefulBlock,
	source []byte,
	status choices.Status,
	vm VM,
) (*StatelessBlock, error) {
	ctx, span := vm.Tracer().Start(ctx, "chain.ParseStatefulBlock")
	defer span.End()

	// Perform basic correctness checks before doing any expensive work
	if blk.Timestamp > time.Now().Add(FutureBound).UnixMilli() {
		return nil, ErrTimestampTooLate
	}

	if len(source) == 0 {
		nsource, err := blk.Marshal()
		if err != nil {
			return nil, err
		}
		source = nsource
	}
	b := &StatelessBlock{
		StatefulBlock: blk,
		t:             time.UnixMilli(blk.Timestamp),
		bytes:         source,
		st:            status,
		vm:            vm,
		id:            utils.ToID(source),
	}

	// If we are parsing an older block, it will not be re-executed and should
	// not be tracked as a parsed block
	lastAccepted := b.vm.LastAcceptedBlock()
	if lastAccepted == nil || blk.Height <= lastAccepted.StatefulBlock.Height { // nil when parsing genesis
		return b, nil
	}

	// Update set (handle this on built)
	b.chunks = set.NewSet[ids.ID](len(blk.AvailableChunks))
	for _, cert := range blk.AvailableChunks {
		b.chunks.Add(cert.Chunk)
	}

	// TODO: add parent, execHeight, and bctx to the block?

	return b, nil
}

// implements "snowman.Block.choices.Decidable"
func (b *StatelessBlock) ID() ids.ID { return b.id }

// implements "block.WithVerifyContext"
//
// TODO: may want to use a different p-chain height to verify things?
func (b *StatelessBlock) ShouldVerifyWithContext(context.Context) (bool, error) {
	// Because we require context to add any new chunks (which contain all txs), we know
	// that any warp messages that are included in these chunks can be verified.
	return len(b.AvailableChunks) > 0 /* need to verify certs */, nil
}

// implements "block.WithVerifyContext"
func (b *StatelessBlock) VerifyWithContext(ctx context.Context, bctx *block.Context) error {
	start := time.Now()
	defer func() {
		b.vm.RecordBlockVerify(time.Since(start))
	}()

	ctx, span := b.vm.Tracer().Start(
		ctx, "StatelessBlock.VerifyWithContext",
		oteltrace.WithAttributes(
			attribute.Int64("height", int64(b.StatefulBlock.Height)),
			attribute.Int64("pchainHeight", int64(bctx.PChainHeight)),
		),
	)
	defer span.End()

	// Persist the context in case we need it during Accept
	b.bctx = bctx

	// Proceed with normal verification
	return b.verify(ctx)
}

// implements "snowman.Block"
func (b *StatelessBlock) Verify(ctx context.Context) error {
	start := time.Now()
	defer func() {
		b.vm.RecordBlockVerify(time.Since(start))
	}()

	ctx, span := b.vm.Tracer().Start(
		ctx, "StatelessBlock.Verify",
		oteltrace.WithAttributes(
			attribute.Int64("height", int64(b.StatefulBlock.Height)),
		),
	)
	defer span.End()

	return b.verify(ctx)
}

// Tasks
// 1) verify certificates (correct signatures, correct weight, not duplicates)
// 2) verify parent state root is correct
// 3) verify executed certificates (correct IDs)
func (b *StatelessBlock) verify(ctx context.Context) error {
	// Skip verification if we built this block
	if b.built {
		return nil
	}

	// TODO: skip verification if state does not exist yet

	var (
		log = b.vm.Logger()
		r   = b.vm.Rules(b.StatefulBlock.Timestamp)
	)

	// Perform basic correctness checks before doing any expensive work
	if b.Timestamp().UnixMilli() > time.Now().Add(FutureBound).UnixMilli() {
		return ErrTimestampTooLate
	}

	// Check that gap between parent is at least minimum
	//
	// We do not have access to state here, so we must use the parent block.
	parent, err := b.vm.GetStatelessBlock(ctx, b.StatefulBlock.Parent)
	if err != nil {
		return err
	}
	if b.StatefulBlock.Height != parent.StatefulBlock.Height+1 {
		return ErrInvalidBlockHeight
	}
	b.parent = parent
	parentTimestamp := parent.StatefulBlock.Timestamp
	if b.StatefulBlock.Timestamp < parentTimestamp+r.GetMinBlockGap() {
		return ErrTimestampTooEarly
	}
	if len(b.AvailableChunks) == 0 && b.StatefulBlock.Timestamp < parentTimestamp+r.GetMinEmptyBlockGap() {
		return ErrTimestampTooEarly
	}

	// Check duplicate certificates
	repeats, err := parent.IsRepeatChunk(ctx, b.AvailableChunks, set.NewBits())
	if err != nil {
		return err
	}
	if repeats.Len() > 0 {
		return errors.New("duplicate chunk issuance")
	}

	// Verify certificates
	if b.bctx != nil {
		// Get validator set at current height
		//
		// TODO: need to handle case where state sync on P-Chain and don't have historical validator set
		vdrSet, err := b.vm.ValidatorState().GetValidatorSet(ctx, b.bctx.PChainHeight, r.ChainID())
		if err != nil {
			return err
		}

		// TODO: replace with canonical function used in proposer monito
		// Compute canonical validator set (already have set, so don't call again)
		// Source: https://github.com/ava-labs/avalanchego/blob/813bd481c764970b5c47c3ae9c0a40f2c28da8e4/vms/platformvm/warp/validator.go#L61-L92
		var (
			vdrs        = make(map[string]*warp.Validator, len(vdrSet))
			totalWeight uint64
		)
		for _, vdr := range vdrSet {
			totalWeight, err = math.Add64(totalWeight, vdr.Weight)
			if err != nil {
				return err
			}

			if vdr.PublicKey == nil {
				continue
			}

			pkBytes := bls.SerializePublicKey(vdr.PublicKey)
			uniqueVdr, ok := vdrs[string(pkBytes)]
			if !ok {
				uniqueVdr = &warp.Validator{
					PublicKey:      vdr.PublicKey,
					PublicKeyBytes: pkBytes,
				}
				vdrs[string(pkBytes)] = uniqueVdr
			}

			uniqueVdr.Weight += vdr.Weight // Impossible to overflow here
			uniqueVdr.NodeIDs = append(uniqueVdr.NodeIDs, vdr.NodeID)
		}
		vdrList := maps.Values(vdrs)
		autils.Sort(vdrList)

		// Verify certificates
		//
		// TODO: make parallel
		// TODO: cache verifications (may be verified multiple times at the same p-chain height while
		// waiting for execution to complete).
		for _, cert := range b.AvailableChunks {
			// TODO: Ensure cert is from a validator
			//
			// Signers verify that validator signed chunk with right key when signing chunk, so may
			// not be necessary?
			//
			// TODO: consider not verifying this in case validator set changes?

			// TODO: consider moving to chunk verify
			// Ensure chunk is not too early
			if cert.Slot > b.StatefulBlock.Timestamp+r.GetValidityWindow() {
				return ErrTimestampTooEarly
			}

			// Ensure chunk is not expired
			if cert.Slot < b.StatefulBlock.Timestamp {
				return ErrTimestampTooLate
			}

			// Ensure chunk expiry is aligned to a tenth of a second
			//
			// Signatures will only be given for a configurable number of chunks per
			// second.
			//
			// TODO: consider moving to unmarshal
			if cert.Slot%consts.MillisecondsPerDecisecond != 0 {
				return ErrMisalignedTime
			}

			// Verify multi-signature
			filteredVdrs, err := warp.FilterValidators(cert.Signers, vdrList)
			if err != nil {
				return err
			}
			filteredWeight, err := warp.SumWeight(filteredVdrs)
			if err != nil {
				return err
			}
			if err := warp.VerifyWeight(filteredWeight, totalWeight, 67, 100); err != nil {
				return err
			}
			aggrPubKey, err := warp.AggregatePublicKeys(filteredVdrs)
			if err != nil {
				return err
			}
			msg, err := cert.Digest()
			if err != nil {
				return err
			}
			if !bls.Verify(aggrPubKey, cert.Signature, msg) {
				return errors.New("certificate invalid")
			}
		}
	} else {
		if len(b.AvailableChunks) > 0 {
			return errors.New("block context not provided but have available chunks")
		}
	}

	// Verify start root and execution results
	depth := r.GetBlockExecutionDepth()
	if b.StatefulBlock.Height < depth {
		if b.StartRoot != ids.Empty || len(b.ExecutedChunks) > 0 {
			return errors.New("no execution result should exist")
		}
	} else {
		execHeight := b.StatefulBlock.Height - depth
		root, executed, err := b.vm.Engine().Results(execHeight)
		if err != nil {
			// TODO: add stat for tracking how often this happens
			// TODOL handle case where we state synced and don't have results
			log.Warn("could not get results for block", zap.Uint64("height", execHeight))
			return err
		}
		if b.StartRoot != root {
			return errors.New("start root mismatch")
		}
		if len(b.ExecutedChunks) != len(executed) {
			return errors.New("executed chunks count mismatch")
		}
		for i, id := range b.ExecutedChunks {
			if id != executed[i] {
				return errors.New("executed chunks mismatch")
			}
		}
		b.execHeight = &execHeight
	}

	b.vm.Verified(ctx, b)
	return nil
}

// implements "snowman.Block.choices.Decidable"
func (b *StatelessBlock) Accept(ctx context.Context) error {
	start := time.Now()
	defer func() {
		b.vm.RecordBlockAccept(time.Since(start))
	}()

	ctx, span := b.vm.Tracer().Start(ctx, "StatelessBlock.Accept")
	defer span.End()

	b.st = choices.Accepted

	// Start async execution
	b.vm.Engine().Execute(b, b.StatefulBlock.Timestamp)

	// Collect async results (if any)
	var filteredChunks []*FilteredChunk
	if b.execHeight != nil {
		_, fc, err := b.vm.Engine().Commit(ctx, *b.execHeight)
		if err != nil {
			return err
		}
		filteredChunks = fc
	}
	b.vm.Accepted(ctx, b, filteredChunks)
	return nil
}

// used for accepting syncable blocks
func (b *StatelessBlock) MarkAccepted(ctx context.Context) {
	// Accept block and free unnecessary memory
	b.st = choices.Accepted

	// [Accepted] will persist the block to disk and set in-memory variables
	// needed to ensure we don't resync all blocks when state sync finishes.
	//
	// Note: We will not call [b.vm.Verified] before accepting during state sync
	// TODO: this is definitely wrong, we should fix it
	b.vm.Accepted(ctx, b, nil)
}

// implements "snowman.Block.choices.Decidable"
func (b *StatelessBlock) Reject(ctx context.Context) error {
	ctx, span := b.vm.Tracer().Start(ctx, "StatelessBlock.Reject")
	defer span.End()

	b.st = choices.Rejected
	b.vm.Rejected(ctx, b)
	return nil
}

// implements "snowman.Block.choices.Decidable"
func (b *StatelessBlock) Status() choices.Status { return b.st }

// implements "snowman.Block"
func (b *StatelessBlock) Parent() ids.ID { return b.StatefulBlock.Parent }

// implements "snowman.Block"
func (b *StatelessBlock) Bytes() []byte { return b.bytes }

// implements "snowman.Block"
func (b *StatelessBlock) Height() uint64 { return b.StatefulBlock.Height }

// implements "snowman.Block"
func (b *StatelessBlock) Timestamp() time.Time { return b.t }

func (b *StatelessBlock) IsRepeatChunk(ctx context.Context, certs []*ChunkCertificate, marker set.Bits) (set.Bits, error) {
	if b.st == choices.Accepted || b.StatefulBlock.Height == 0 /* genesis */ {
		return b.vm.IsRepeatChunk(ctx, certs, marker), nil
	}
	for i, cert := range certs {
		if marker.Contains(i) {
			continue
		}
		if b.chunks.Contains(cert.Chunk) {
			marker.Add(i)
		}
	}
	parent, err := b.vm.GetStatelessBlock(ctx, b.StatefulBlock.Parent)
	if err != nil {
		return marker, err
	}
	return parent.IsRepeatChunk(ctx, certs, marker)
}

type SyncableBlock struct {
	*StatelessBlock
}

func (sb *SyncableBlock) Accept(ctx context.Context) (block.StateSyncMode, error) {
	return sb.vm.AcceptedSyncableBlock(ctx, sb)
}

func NewSyncableBlock(sb *StatelessBlock) *SyncableBlock {
	return &SyncableBlock{sb}
}

func (sb *SyncableBlock) String() string {
	return fmt.Sprintf("%d:%s root=%s", sb.Height(), sb.ID(), sb.StartRoot)
}
