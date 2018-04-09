package main

import (
	"testing"
)

func BenchmarkEfaceScalar(b *testing.B) {
	var Uint uint32
	b.Run("uint32", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// MOVL DX, (AX)
			Uint = uint32(i)
		}
	})
	var Eface interface{}
	b.Run("eface32", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// MOVL  CX, ""..autotmp_3+36(SP)
			// LEAQ  type.uint32(SB), AX
			// MOVQ  AX, (SP)
			// LEAQ  ""..autotmp_3+36(SP), DX
			// MOVQ  DX, 8(SP)
			// CALL  runtime.convT2E32(SB)
			// MOVQ  24(SP), AX
			// MOVQ  16(SP), CX
			// MOVQ  "".&Eface+48(SP), DX
			// MOVQ  CX, (DX)
			// MOVL  runtime.writeBarrier(SB), CX
			// LEAQ  8(DX), DI
			// TESTL CX, CX
			// JNE   148
			// MOVQ  AX, 8(DX)
			// JMP   46
			// CALL  runtime.gcWriteBarrier(SB)
			// JMP   46
			Eface = uint32(i)
		}
	})
	b.Run("eface8", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// LEAQ    type.uint8(SB), BX
			// MOVQ    BX, (CX)
			// MOVBLZX AL, SI
			// LEAQ    runtime.staticbytes(SB), R8
			// ADDQ    R8, SI
			// MOVL    runtime.writeBarrier(SB), R9
			// LEAQ    8(CX), DI
			// TESTL   R9, R9
			// JNE     100
			// MOVQ    SI, 8(CX)
			// JMP     40
			// MOVQ    AX, R9
			// MOVQ    SI, AX
			// CALL    runtime.gcWriteBarrier(SB)
			// MOVQ    R9, AX
			// JMP     40
			Eface = uint8(i)
		}
	})
	b.Run("eface-zeroval", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// MOVL  $0, ""..autotmp_3+36(SP)
			// LEAQ  type.uint32(SB), AX
			// MOVQ  AX, (SP)
			// LEAQ  ""..autotmp_3+36(SP), CX
			// MOVQ  CX, 8(SP)
			// CALL  runtime.convT2E32(SB)
			// MOVQ  16(SP), AX
			// MOVQ  24(SP), CX
			// MOVQ  "".&Eface+48(SP), DX
			// MOVQ  AX, (DX)
			// MOVL  runtime.writeBarrier(SB), AX
			// LEAQ  8(DX), DI
			// TESTL AX, AX
			// JNE   152
			// MOVQ  CX, 8(DX)
			// JMP   46
			// MOVQ  CX, AX
			// CALL  runtime.gcWriteBarrier(SB)
			// JMP   46
			Eface = uint32(i - i) // outsmart the compiler
		}
	})
	b.Run("eface-static", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// LEAQ  type.uint64(SB), BX
			// MOVQ  BX, (CX)
			// MOVL  runtime.writeBarrier(SB), SI
			// LEAQ  8(CX), DI
			// TESTL SI, SI
			// JNE   92
			// LEAQ  "".statictmp_0(SB), SI
			// MOVQ  SI, 8(CX)
			// JMP   40
			// MOVQ  AX, SI
			// LEAQ  "".statictmp_0(SB), AX
			// CALL  runtime.gcWriteBarrier(SB)
			// MOVQ  SI, AX
			// LEAQ  "".statictmp_0(SB), SI
			// JMP   40
			Eface = uint64(42)
		}
	})
}

func main() {
	// So that we can easily compile this and retrieve `main.statictmp_0`
	// from the final executable.
	BenchmarkEfaceScalar(&testing.B{})
}
