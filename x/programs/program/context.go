// Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package program

import (
	"github.com/ava-labs/hypersdk/codec"
)

type Context struct {
	ProgramID codec.LID `json:"program"`
	// Actor            [32]byte `json:"actor"`
	// OriginatingActor [32]byte `json:"originating_actor"`
}
