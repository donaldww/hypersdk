// Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package actions

import (
	"context"

	"github.com/ava-labs/avalanchego/ids"
	smath "github.com/ava-labs/avalanchego/utils/math"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/consts"
	"github.com/ava-labs/hypersdk/examples/tokenvm/storage"
	"github.com/ava-labs/hypersdk/state"
	"github.com/ava-labs/hypersdk/utils"
)

var _ chain.Action = (*MintAsset)(nil)

type MintAsset struct {
	// To is the recipient of the [Value].
	To codec.Address `json:"to"`

	// Asset is the [ActionID] that created the asset.
	Asset codec.LID `json:"asset"`

	// Number of assets to mint to [To].
	Value uint64 `json:"value"`
}

func (*MintAsset) GetTypeID() uint8 {
	return mintAssetID
}

func (*MintAsset) GetActionID(i uint8, txID ids.ID) codec.LID {
	return codec.CreateLID(i, txID)
}

func (m *MintAsset) StateKeys(codec.Address, codec.LID) state.Keys {
	return state.Keys{
		string(storage.AssetKey(m.Asset)):         state.Read | state.Write,
		string(storage.BalanceKey(m.To, m.Asset)): state.All,
	}
}

func (*MintAsset) StateKeysMaxChunks() []uint16 {
	return []uint16{storage.AssetChunks, storage.BalanceChunks}
}

func (m *MintAsset) Execute(
	ctx context.Context,
	_ chain.Rules,
	mu state.Mutable,
	_ int64,
	actor codec.Address,
	_ codec.LID,
) (bool, uint64, [][]byte, error) {
	if m.Asset == codec.EmptyAddress {
		return false, MintAssetComputeUnits, [][]byte{OutputAssetIsNative}, nil
	}
	if m.Value == 0 {
		return false, MintAssetComputeUnits, [][]byte{OutputValueZero}, nil
	}
	exists, symbol, decimals, metadata, supply, owner, err := storage.GetAsset(ctx, mu, m.Asset)
	if err != nil {
		return false, MintAssetComputeUnits, [][]byte{utils.ErrBytes(err)}, nil
	}
	if !exists {
		return false, MintAssetComputeUnits, [][]byte{OutputAssetMissing}, nil
	}
	if owner != actor {
		return false, MintAssetComputeUnits, [][]byte{OutputWrongOwner}, nil
	}
	newSupply, err := smath.Add64(supply, m.Value)
	if err != nil {
		return false, MintAssetComputeUnits, [][]byte{utils.ErrBytes(err)}, nil
	}
	if err := storage.SetAsset(ctx, mu, m.Asset, symbol, decimals, metadata, newSupply, actor); err != nil {
		return false, MintAssetComputeUnits, [][]byte{utils.ErrBytes(err)}, nil
	}
	if err := storage.AddBalance(ctx, mu, m.To, m.Asset, m.Value, true); err != nil {
		return false, MintAssetComputeUnits, [][]byte{utils.ErrBytes(err)}, nil
	}
	return true, MintAssetComputeUnits, [][]byte{{}}, nil
}

func (*MintAsset) MaxComputeUnits(chain.Rules) uint64 {
	return MintAssetComputeUnits
}

func (*MintAsset) Size() int {
	return codec.AddressLen + consts.IDLen + consts.Uint64Len
}

func (m *MintAsset) Marshal(p *codec.Packer) {
	p.PackAddress(m.To)
	p.PackActionID(m.Asset)
	p.PackUint64(m.Value)
}

func UnmarshalMintAsset(p *codec.Packer) (chain.Action, error) {
	var mint MintAsset
	p.UnpackAddress(&mint.To)
	p.UnpackActionID(true, &mint.Asset) // empty ID is the native asset
	mint.Value = p.UnpackUint64(true)
	return &mint, p.Err()
}

func (*MintAsset) ValidRange(chain.Rules) (int64, int64) {
	// Returning -1, -1 means that the action is always valid.
	return -1, -1
}
