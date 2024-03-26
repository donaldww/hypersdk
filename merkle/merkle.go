package merkle

import (
	"context"
	"slices"
	"sync"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/maybe"
	"github.com/ava-labs/avalanchego/x/merkledb"
	ssync "github.com/ava-labs/avalanchego/x/sync"
	"github.com/ava-labs/hypersdk/smap"
	"github.com/ava-labs/hypersdk/state"
)

var (
	_ state.Immutable = (*Merkle)(nil)
	_ state.Mutable   = (*Merkle)(nil)
	_ state.Database  = (*Merkle)(nil)

	_ ssync.DB = (*Merkle)(nil)
)

const (
	stateInitialSize   = 10_000_000
	pendingInitialSize = 1_000_000
)

type Merkle struct {
	state   map[string][]byte
	mdb     merkledb.MerkleDB
	pending map[string]maybe.Maybe[[]byte]

	l  sync.RWMutex
	cl sync.Mutex
}

func New(ctx context.Context, db database.Database, cfg merkledb.Config) (*Merkle, error) {
	mdb, err := merkledb.New(ctx, db, cfg)
	if err != nil {
		return nil, err
	}
	// TODO: load values into [state]
	return &Merkle{
		state:   make(map[string][]byte, stateInitialSize),
		mdb:     mdb,
		pending: make(map[string]maybe.Maybe[[]byte], pendingInitialSize),
	}, nil
}

func (m *Merkle) Update(_ context.Context, ops *smap.SMap[maybe.Maybe[[]byte]]) {
	m.l.Lock()
	defer m.l.Unlock()

	ops.Iterate(func(key string, value maybe.Maybe[[]byte]) bool {
		m.pending[key] = value
		if value.IsNothing() {
			delete(m.state, key)
		} else {
			m.state[key] = value.Value()
		}
		return true
	})
}

func (m *Merkle) PrepareCommit(context.Context) func(context.Context) (ids.ID, error) {
	m.l.Lock()
	defer m.l.Unlock()

	pending := m.pending
	m.pending = make(map[string]maybe.Maybe[[]byte], pendingInitialSize)
	m.cl.Lock()
	return func(ctx context.Context) (ids.ID, error) {
		defer m.cl.Unlock()

		// We don't consume bytes because we don't pre-copy them into [pending] (in case
		// they are later replaced).
		view, err := m.mdb.NewView(ctx, merkledb.ViewChanges{MapOps: pending})
		if err != nil {
			return ids.Empty, err
		}
		if err := view.CommitToDB(ctx); err != nil {
			return ids.Empty, err
		}
		return m.mdb.GetMerkleRoot(ctx)
	}
}

// We assume that any bytes provided to Insert can be consumed.
func (m *Merkle) Insert(_ context.Context, key, value []byte) error {
	m.l.Lock()
	defer m.l.Unlock()

	m.pending[string(key)] = maybe.Some(value)
	m.state[string(key)] = value
	return nil
}

func (m *Merkle) Remove(_ context.Context, key []byte) error {
	m.l.Lock()
	defer m.l.Unlock()

	m.pending[string(key)] = maybe.Nothing[[]byte]()
	delete(m.state, string(key))
	return nil
}

// Any bytes returned by [GetValue] can be modified.
func (m *Merkle) GetValue(_ context.Context, key []byte) ([]byte, error) {
	m.l.RLock()
	defer m.l.RUnlock()

	value, ok := m.state[string(key)]
	if !ok {
		return nil, database.ErrNotFound
	}
	return slices.Clone(value), nil
}

// Any bytes returned by [GetValues] can be modified.
func (m *Merkle) GetValues(_ context.Context, keys [][]byte) ([][]byte, []error) {
	m.l.RLock()
	defer m.l.RUnlock()

	var (
		values = make([][]byte, len(keys))
		errors = make([]error, len(keys))
	)
	for i, key := range keys {
		value, ok := m.state[string(key)]
		if !ok {
			errors[i] = database.ErrNotFound
		} else {
			values[i] = slices.Clone(value)
		}
	}
	return values, errors
}

// Implement [sync.DB] interface
func (m *Merkle) Clear() error {
	m.l.Lock()
	defer m.l.Unlock()

	m.state = make(map[string][]byte, stateInitialSize)
	m.pending = make(map[string]maybe.Maybe[[]byte], pendingInitialSize)
	return m.mdb.Clear()
}

func (m *Merkle) GetMerkleRoot(_ context.Context) (ids.ID, error) {
	return m.mdb.GetMerkleRoot(context.Background())
}

func (m *Merkle) GetProof(ctx context.Context, keyBytes []byte) (*merkledb.Proof, error) {
	return m.mdb.GetProof(ctx, keyBytes)
}

func (m *Merkle) GetChangeProof(
	ctx context.Context,
	startRootID ids.ID,
	endRootID ids.ID,
	start maybe.Maybe[[]byte],
	end maybe.Maybe[[]byte],
	maxLength int,
) (*merkledb.ChangeProof, error) {
	return m.mdb.GetChangeProof(ctx, startRootID, endRootID, start, end, maxLength)
}

func (m *Merkle) VerifyChangeProof(
	ctx context.Context,
	proof *merkledb.ChangeProof,
	start maybe.Maybe[[]byte],
	end maybe.Maybe[[]byte],
	expectedEndRootID ids.ID,
) error {
	return m.mdb.VerifyChangeProof(ctx, proof, start, end, expectedEndRootID)
}

func (m *Merkle) GetRangeProofAtRoot(
	ctx context.Context,
	rootID ids.ID,
	start maybe.Maybe[[]byte],
	end maybe.Maybe[[]byte],
	maxLength int,
) (*merkledb.RangeProof, error) {
	return m.mdb.GetRangeProofAtRoot(ctx, rootID, start, end, maxLength)
}

func (m *Merkle) CommitRangeProof(ctx context.Context, start, end maybe.Maybe[[]byte], proof *merkledb.RangeProof) error {
	return m.mdb.CommitRangeProof(ctx, start, end, proof)
}

func (m *Merkle) CommitChangeProof(ctx context.Context, proof *merkledb.ChangeProof) error {
	return m.mdb.CommitChangeProof(ctx, proof)
}

func (m *Merkle) Close() error {
	return m.mdb.Close()
}