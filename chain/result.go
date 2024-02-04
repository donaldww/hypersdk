// Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/consts"
)

type Result struct {
	Valid   bool
	Success bool
	Output  []byte

	Consumed Dimensions
	Fee      uint64

	WarpMessage *warp.UnsignedMessage
}

func (r *Result) Size() int {
	size := consts.BoolLen + consts.BoolLen + codec.BytesLen(r.Output) + DimensionsLen + consts.Uint64Len
	if r.WarpMessage != nil {
		size += codec.BytesLen(r.WarpMessage.Bytes())
	} else {
		size += codec.BytesLen(nil)
	}
	return size
}

func (r *Result) Marshal(p *codec.Packer) error {
	p.PackBool(r.Valid)
	p.PackBool(r.Success)
	p.PackBytes(r.Output)
	p.PackFixedBytes(r.Consumed.Bytes())
	p.PackUint64(r.Fee)
	var warpBytes []byte
	if r.WarpMessage != nil {
		warpBytes = r.WarpMessage.Bytes()
	}
	p.PackBytes(warpBytes)
	return nil
}

func MarshalResults(src []*Result) ([]byte, error) {
	size := consts.IntLen + codec.CummSize(src)
	p := codec.NewWriter(size, consts.MaxInt) // could be much larger than [NetworkSizeLimit]
	p.PackInt(len(src))
	for _, result := range src {
		if err := result.Marshal(p); err != nil {
			return nil, err
		}
	}
	return p.Bytes(), p.Err()
}

func UnmarshalResult(p *codec.Packer) (*Result, error) {
	result := &Result{
		Valid:   p.UnpackBool(),
		Success: p.UnpackBool(),
	}
	p.UnpackBytes(consts.MaxInt, false, &result.Output)
	if len(result.Output) == 0 {
		// Enforce object standardization
		result.Output = nil
	}
	consumedRaw := make([]byte, DimensionsLen)
	p.UnpackFixedBytes(DimensionsLen, &consumedRaw)
	consumed, err := UnpackDimensions(consumedRaw)
	if err != nil {
		return nil, err
	}
	result.Consumed = consumed
	result.Fee = p.UnpackUint64(false)
	var warpMessage []byte
	p.UnpackBytes(MaxWarpMessageSize, false, &warpMessage)
	if len(warpMessage) > 0 {
		msg, err := warp.ParseUnsignedMessage(warpMessage)
		if err != nil {
			return nil, err
		}
		result.WarpMessage = msg
	}
	return result, p.Err()
}

func UnmarshalResults(src []byte) ([]*Result, error) {
	p := codec.NewReader(src, consts.MaxInt) // could be much larger than [NetworkSizeLimit]
	items := p.UnpackInt(false)
	results := make([]*Result, items)
	for i := 0; i < items; i++ {
		result, err := UnmarshalResult(p)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	if !p.Empty() {
		return nil, ErrInvalidObject
	}
	return results, nil
}
