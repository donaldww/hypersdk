// Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package examples

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/hypersdk/x/programs/engine"
	programImport "github.com/ava-labs/hypersdk/x/programs/examples/imports/program"
	"github.com/ava-labs/hypersdk/x/programs/examples/imports/pstate"
	"github.com/ava-labs/hypersdk/x/programs/examples/storage"
	"github.com/ava-labs/hypersdk/x/programs/host"
	"github.com/ava-labs/hypersdk/x/programs/program"
	"github.com/ava-labs/hypersdk/x/programs/runtime"
	"github.com/ava-labs/hypersdk/x/programs/tests"
)

// go test -v -timeout 30s -run ^TestCounterProgram$ github.com/ava-labs/hypersdk/x/programs/examples
func TestCounterProgram(t *testing.T) {
	require := require.New(t)
	db := newTestDB()
	maxUnits := uint64(10000000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := runtime.NewConfig()
	log := logging.NewLogger(
		"",
		logging.NewWrappedCore(
			logging.Info,
			os.Stderr,
			logging.Plain.ConsoleEncoder(),
		))

	eng := engine.New(engine.NewConfig())
	reentrancyGuard := program.NewReentrancyGuard()
	// define supported imports
	importsBuilder := host.NewImportsBuilder()
	importsBuilder.Register("state", func() host.Import {
		return pstate.New(log, db)
	})
	importsBuilder.Register("program", func() host.Import {
		return programImport.New(log, eng, db, cfg, reentrancyGuard)
	})
	imports := importsBuilder.Build()

	wasmBytes := tests.ReadFixture(t, "../tests/fixture/counter.wasm")
	rt := runtime.New(log, eng, imports, cfg, reentrancyGuard)
	err := rt.Initialize(ctx, wasmBytes, maxUnits)
	require.NoError(err)

	balance, err := rt.Meter().GetBalance()
	require.NoError(err)
	require.Equal(maxUnits, balance)

	// simulate create program transaction
	programID := ids.GenerateTestID()
	err = storage.SetProgram(ctx, db, programID, wasmBytes)
	require.NoError(err)

	mem, err := rt.Memory()
	require.NoError(err)
	programIDPtr, err := argumentToSmartPtr(programID, mem)
	require.NoError(err)

	// generate alice keys
	_, aliceKey, err := newKey()
	require.NoError(err)

	// write alice's key to stack and get pointer
	alicePtr, err := argumentToSmartPtr(aliceKey, mem)
	require.NoError(err)

	// create counter for alice on program 1
	result, err := rt.Call(ctx, "initialize_address", programIDPtr, alicePtr)
	require.NoError(err)
	require.Equal(int64(1), result[0])

	// validate counter at 0
	result, err = rt.Call(ctx, "get_value", programIDPtr, alicePtr)
	require.NoError(err)
	require.Equal(int64(0), result[0])

	// simulate creating second program transaction
	program2ID := ids.GenerateTestID()
	err = storage.SetProgram(ctx, db, program2ID, wasmBytes)
	require.NoError(err)

	mem2, err := rt.Memory()
	require.NoError(err)
	programID2Ptr, err := argumentToSmartPtr(program2ID, mem2)
	require.NoError(err)

	// initialize counter for alice on runtime 2
	result, err = rt.Call(ctx, "initialize_address", programID2Ptr, alicePtr)
	require.NoError(err)
	require.Equal(int64(1), result[0])

	// increment alice's counter on program 2 by 10
	incAmount := int64(10)
	incAmountPtr, err := argumentToSmartPtr(incAmount, mem2)
	require.NoError(err)
	result, err = rt.Call(ctx, "inc", programID2Ptr, alicePtr, incAmountPtr)

	require.NoError(err)
	require.Equal(int64(1), result[0])

	result, err = rt.Call(ctx, "get_value", programID2Ptr, alicePtr)
	require.NoError(err)
	require.Equal(incAmount, result[0])

	// increment alice's counter on program 1
	onePtr, err := argumentToSmartPtr(int64(1), mem)
	require.NoError(err)
	result, err = rt.Call(ctx, "inc", programIDPtr, alicePtr, onePtr)

	require.NoError(err)
	require.Equal(int64(1), result[0])

	result, err = rt.Call(ctx, "get_value", programIDPtr, alicePtr)
	require.NoError(err)

	log.Debug("count program 1",
		zap.Int64("alice", result[0]),
	)

	// write program id 2 to stack of program 1
	programID2Ptr, err = argumentToSmartPtr(program2ID, mem)
	require.NoError(err)

	caller := programIDPtr
	target := programID2Ptr
	maxUnitsProgramToProgram := int64(1000000)
	maxUnitsProgramToProgramPtr, err := argumentToSmartPtr(maxUnitsProgramToProgram, mem)
	require.NoError(err)

	// increment alice's counter on program 2
	fivePtr, err := argumentToSmartPtr(int64(5), mem)
	require.NoError(err)
	result, err = rt.Call(ctx, "inc_external", caller, target, maxUnitsProgramToProgramPtr, alicePtr, fivePtr)
	require.NoError(err)
	require.Equal(int64(1), result[0])

	// expect alice's counter on program 2 to be 15
	result, err = rt.Call(ctx, "get_value_external", caller, target, maxUnitsProgramToProgramPtr, alicePtr)
	require.NoError(err)
	require.Equal(int64(15), result[0])

	threePtr, err := argumentToSmartPtr(int64(3), mem)
	require.NoError(err)
	// increment by 3 using a reentrant function
	_, err = rt.Call(ctx, "multiply_reentrant", target, alicePtr, onePtr, threePtr, maxUnitsProgramToProgramPtr)
	require.NoError(err)

	result, err = rt.Call(ctx, "get_value", target, alicePtr)
	require.NoError(err)
	require.Equal(int64(18), result[0])

	// This should fail because it is a reentrant call that is not allowed
	_, err = rt.Call(ctx, "multiply", target, alicePtr, onePtr, threePtr, maxUnitsProgramToProgramPtr)
	require.Error(err)

	balance, err = rt.Meter().GetBalance()
	require.NoError(err)
	require.Greater(balance, uint64(0))

}
