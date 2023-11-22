package imports

import (
	"github.com/bytecodealliance/wasmtime-go/v14"
)

type ValKind = wasmtime.ValKind

const TypeI64 ValKind = wasmtime.KindI64
const TypeI32 ValKind = wasmtime.KindI32

type Val struct {
	inner wasmtime.Val
}

func (v Val) I32() int32 {
	if v.Kind() != wasmtime.KindI32 {
		panic("not an i32")
	}
	return v.inner.I32()
}

// I64 returns the underlying 64-bit integer if this is an `i64`, or panics.
func (v Val) I64() int64 {
	if v.Kind() != wasmtime.KindI64 {
		panic("not an i64")
	}
	return v.inner.I64()
}

func (v Val) Kind() ValKind {
	switch v.inner.Kind() {
	case wasmtime.KindI32:
		return TypeI32
	case wasmtime.KindI64:
		return TypeI64
	default:
		panic("unknown val kind")
	}
}

// ValI32 converts a int32 to a i32 Val
func ValI32(val int32) Val {
	return Val{inner: wasmtime.ValI32(val)}
}

// ValI64 converts a go int64 to a i64 Val
func ValI64(val int64) Val {
	return Val{inner: wasmtime.ValI64(val)}
}

// Breaking this out into a separate interfaces allows us to avoid reflection and
// use concrete types.

type OneParam[T any] interface {
	Call(*T, int64) (Val, error)
}

type OneParamFn[T any] func(*T, int64) (Val, error)

func (fn OneParamFn[T]) Call(t *T, arg1 int64) (Val, error) {
	return fn(t, arg1)
}

type TwoParam[T any] interface {
	Call(*T, int64, int64) (Val, error)
}

type TwoParamFn[T any] func(*T, int64, int64) (Val, error)

func (fn TwoParamFn[T]) Call(t *T, arg1, arg2 int64) (Val, error) {
	return fn(t, arg1, arg2)
}

type ThreeParam[T any] interface {
	Call(*T, int64, int64, int64) (Val, error)
}

type ThreeParamFn[T any] func(*T, int64, int64, int64) (Val, error)

func (fn ThreeParamFn[T]) Call(t *T, arg1, arg2, arg3 int64) (Val, error) {
	return fn(t, arg1, arg2, arg3)
}

type FourParam[T any] interface {
	Call(*T, int64, int64, int64, int64) (Val, error)
}

type FourParamFn[T any] func(*T, int64, int64, int64, int64) (Val, error)

func (fn FourParamFn[T]) Call(t *T, arg1, arg2, arg3, arg4 int64) (Val, error) {
	return fn(t, arg1, arg2, arg3, arg4)
}

type FiveParam[T any] interface {
	Call(*T, int64, int64, int64, int64, int64) (Val, error)
}

type FiveParamFn[T any] func(*T, int64, int64, int64, int64, int64) (Val, error)

func (fn FiveParamFn[T]) Call(t *T, arg1, arg2, arg3, arg4, arg5 int64) (Val, error) {
	return fn(t, arg1, arg2, arg3, arg4, arg5)
}

type SixParam[T any] interface {
	Call(*T, int64, int64, int64, int64, int64, int64) (Val, error)
}

type SixParamFn[T any] func(t *T, arg1, arg2, arg3, arg4, arg5, arg6 int64) (Val, error)

func (fn SixParamFn[T]) Call(t *T, arg1, arg2, arg3, arg4, arg5, arg6 int64) (Val, error) {
	return fn(t, arg1, arg2, arg3, arg4, arg5, arg6)
}