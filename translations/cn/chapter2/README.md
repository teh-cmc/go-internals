<!-- Copyright © 2018 Clement Rey <cr.rey.clement@gmail.com>. -->
<!-- Licensed under the BY-NC-SA Creative Commons 4.0 International Public License. -->

```Bash
$ go version
go version go1.10 linux/amd64
```

# Chapter II: Interfaces

本章覆盖 GO 的 interface 内部实现。

我们主要关注：
- 函数和方法在运行时如何被调用。
- interface 如何构建，其内容如何组成。
- 动态分发是如何实现的，什么时候进行，并且有什么样的调用成本。
- 空接口和其它特殊情况有什么异同。
- 怎么组合 interface 完成工作。
- 如何进行断言，断言的成本有多高。

随着我们越来越深入的挖掘，我们将会研究各种各样的底层知识，比如现代 CPU 的实现细节，Go 编译器的各种优化手段。

---

**Table of Contents**
<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->


- [Function and method calls](#function-and-method-calls)
  - [Overview of direct calls](#overview-of-direct-calls)
  - [Implicit dereferencing](#implicit-dereferencing)
- [Anatomy of an interface](#anatomy-of-an-interface)
  - [Overview of the datastructures](#overview-of-the-datastructures)
  - [Creating an interface](#creating-an-interface)
  - [Reconstructing an `itab` from an executable](#reconstructing-an-itab-from-an-executable)
- [Dynamic dispatch](#dynamic-dispatch)
  - [Indirect method call on interface](#indirect-method-call-on-interface)
  - [Overhead](#overhead)
    - [The theory: quick refresher on modern CPUs](#the-theory-quick-refresher-on-modern-cpus)
    - [The practice: benchmarks](#the-practice-benchmarks)
- [Special cases & compiler tricks](#special-cases--compiler-tricks)
  - [The empty interface](#the-empty-interface)
  - [Interface holding a scalar type](#interface-holding-a-scalar-type)
  - [A word about zero-values](#a-word-about-zero-values)
  - [A tangent about zero-size variables](#a-tangent-about-zero-size-variables)
- [Interface composition](#interface-composition)
- [Assertions](#assertions)
  - [Type assertions](#type-assertions)
  - [Type-switches](#type-switches)
- [Conclusion](#conclusion)
- [Links](#links)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

---

- *本章假设你已经对 Go 的汇编器比较熟悉了 ([chapter I](../chapter1_assembly_primer/README.md)).*
- *当你需要开始研究架构相关的内容时，请假设自己是在使用 `linux/amd64`.*
- *我们会始终保持编译器优化是 **打开** 状态。*
- *引用部分和注释内容都来自于官方文档(包括 Russ Cox "Function Call" 设计文档) 以及代码库，除非特别说明。*

## Function and method calls

正如 Russ Cox 在他函数调用的设计文档所指出的一样(本章最后有链接)，Go 有:

..4 种不同类型的函数..:
> - 顶级函数
> - 值 receiver 的方法
> - 指针 receiver 的方法
> - 函数字面量

..5 种不同类型的调用:
> - 直接调用顶级函数(`func TopLevel(x int){}`)
> - 直接调用值 receiver 的方法(`func (Value) M(int) {}`)
> - 直接调用指针 receiver 的方法(`func (*Pointer) M(int) {}`)
> - 间接调用接口的方法(`type Interface interface { M(int) }`)
> - 间接调用函数值(`var literal = func(x int) {}`)

混合一下，有 10 种可能的函数以及调用类型组合:
> - 直接调用顶级函数 /
> - 直接调用一个值 receiver 的方法 /
> - 直接调用一个指针 receiver 的方法 /
> - 间接调用一个 interface 的方法 / 包含有值方法的值
> - 间接调用一个 interface 的方法 / 包含有值方法的指针
> - 间接调用一个 interface 的方法 / 包含有指针方法的指针
> - 间接调用方法值 / 该值等于顶级方法
> - 间接调用方法值 / 该值等于值方法
> - 间接调用方法值 / 该值等于指针方法
> - 间接调用方法值 / 该值等于函数字面量
>
> (这里用斜线来分离编译时和运行时才能知道的信息。)

本章先来复习一下三种直接调用，然后再把注意力转移到 interface 和间接的方法调用上。

本章不会覆盖函数字面量的内容，因为研究这方面的内容需要我们对闭包技术比较熟悉..而了解闭包可能还需要花费更多的时间。

### Overview of direct calls

思考一下下面的代码 ([direct_calls.go](./direct_calls.go)):
```Go
//go:noinline
func Add(a, b int32) int32 { return a + b }

type Adder struct{ id int32 }
//go:noinline
func (adder *Adder) AddPtr(a, b int32) int32 { return a + b }
//go:noinline
func (adder Adder) AddVal(a, b int32) int32 { return a + b }

func main() {
    Add(10, 32) // direct call of top-level function

    adder := Adder{id: 6754}
    adder.AddPtr(10, 32) // direct call of method with pointer receiver
    adder.AddVal(10, 32) // direct call of method with value receiver

    (&adder).AddVal(10, 32) // implicit dereferencing
}
```

看一看这四种调用生成的代码。

**Direct call of a top-level function**

看看 `Add(10, 32)` 的汇编输出:
```Assembly
0x0000 TEXT	"".main(SB), $40-0
  ;; ...omitted everything but the actual function call...
  0x0021 MOVQ	$137438953482, AX
  0x002b MOVQ	AX, (SP)
  0x002f CALL	"".Add(SB)
  ;; ...omitted everything but the actual function call...
```
从第一章我们已经知道，函数调用会被翻译成直接跳转指令，目标是 `.text` 段的全局函数符号，参数和返回值会被存储在发起调用者的栈帧上。

这个过程比较直观。

Russ Cox 在它的文档里这样概括这件事:
> Direct call of top-level func:
> A direct call of a top-level func passes all arguments on the stack, expecting results to occupy the successive stack positions.

**Direct call of a method with pointer receiver**

先说重要的，receiver 是通过 `adder := Adder{id: 6754}` 来初始化的:
```Assembly
0x0034 MOVL	$6754, "".adder+28(SP)
```
*(我们栈帧上额外的空间是作为帧指针前导的一部分，被预先分配好的，简洁起见，这里没有展示出来。)*

然后是对 `adder.AddPtr(10 32)` 的直接方法调用:
```Assembly
0x0057 LEAQ	"".adder+28(SP), AX	;; move &adder to..
0x005c MOVQ	AX, (SP)		;; ..the top of the stack (argument #1)
0x0060 MOVQ	$137438953482, AX	;; move (32,10) to..
0x006a MOVQ	AX, 8(SP)		;; ..the top of the stack (arguments #3 & #2)
0x006f CALL	"".(*Adder).AddPtr(SB)
```

观察汇编的输出，我们能够清楚地看到对方法的调用(无论 receiver 是值类型还是指针类型)和对函数的调用是相同的，唯一的区别是 receiver 会被当作第一个参数传入。

这种情况下，我们使用 loading the effective address (`LEAQ`) 这条指令来将 `"".adder+28(SP)` 加载到栈帧顶部，所以第一个参数 #1 就变成了 `&adder` (如果你对 `LEA` 和 `MOV` 有一些迷惑，你可能需要看看附录里的资料了)。

同时注意无论 receiver 的类型是值或是指针，编译器是怎么将其编码成符号名:`"".(*Adder).AddPtr` 的。

> Direct call of method:
> In order to use the same generated code for both an indirect call of a func value and for a direct call, the code generated for a method (both value and pointer receivers) is chosen to have the same calling convention as a top-level function with the receiver as a leading argument.

**Direct call of a method with value receiver**

如我们所料，当 receiver 是值类型时，生成的代码和上面的类似。
来看看 `adder.AddVal(10, 32)`:
```Assembly
0x003c MOVQ	$42949679714, AX	;; move (10,6754) to..
0x0046 MOVQ	AX, (SP)		;; ..the top of the stack (arguments #2 & #1)
0x004a MOVL	$32, 8(SP)		;; move 32 to the top of the stack (argument #3)
0x0052 CALL	"".Adder.AddVal(SB)
```

不过这里有一点 trick 的地方，生成的汇编代码没有什么地方有对 `"".adder+28(SP)` 的引用，尽管这个地址是我们 receiver 所在的地址位置。

这是怎么回事呢？因为 receiver 是值类型，且编译器能够通过静态分析推测出其值，这种情况下编译器认为不需要对值从它原来的位置(`28(SP)`)进行拷贝了: 相应的，只要简单的在栈上创建一个新的和 `Adder` 相等的值，把这个操作和传第二个参数的操作进行捆绑，还可以节省一条汇编指令。

再次仔细观察，这个方法的符号名字，显式地指明了它接收的是一个值类型的 receiver。

### Implicit dereferencing

还有最后一种调用 `(&adder).AddVal(10, 32)`。

这种情况，我们使用了一个指针变量来调用一个期望 receiver 是值类型的方法。Go 魔法般地自动自动帮我们把指针解引用并执行了调用。为什么会这样？

编译器如何处理这种情况取决于 receiver 是否逃逸到堆上。

**Case A: receiver 在栈上**

如果 receiver 在栈上，且 receiver 本身很小，这种情况只需要很少的汇编指令就可以将其值拷贝到栈顶然后再对 `"".Adder.AddVal` 进行一次直接的方法调用 (这里指的是值类型的 receiver)。

`(&adder).AddVal(10, 32)` 于是就和下面这种情况很相似了:
```Assembly
0x0074 MOVL	"".adder+28(SP), AX	;; move (i.e. copy) adder (note the MOV instead of a LEA) to..
0x0078 MOVL	AX, (SP)		;; ..the top of the stack (argument #1)
0x007b MOVQ	$137438953482, AX	;; move (32,10) to..
0x0085 MOVQ	AX, 4(SP)		;; ..the top of the stack (arguments #3 & #2)
0x008a CALL	"".Adder.AddVal(SB)
```

case B 尽管比较高效，但研究起来比较烦人。不过还是来看一下吧。

**Case B: receiver 在堆上**

receiver 逃逸到堆上的话，编译器需要用更聪明的过程来解决问题了: 先生成一个新方法(该方法 receiver 为指针类型，原始方法 receiver 为值类型)，然后用新方法包装原来的 `"".Adder.AddVal`，然后将对原始方法`"".Adder.AddVal`的调用替换为对新方法 `"".(*Adder).AddVal` 的调用。
包装方法唯一的任务，就是保证 receiver 被正确的解引用，并将解引用后的值和其它参数以及返回值在原始方法和调用方法之间拷贝来拷贝去。

(*NOTE: 在汇编输出中，我们生成的这个包装方法都会被标记上 `<autogenerated>`.*)

下面的汇编代码对整个包装方法的过程进行了注释，应该能帮你搞明白这个过程:
```Assembly
0x0000 TEXT	"".(*Adder).AddVal(SB), DUPOK|WRAPPER, $32-24
  ;; ...omitted preambles...

  0x0026 MOVQ	""..this+40(SP), AX ;; check whether the receiver..
  0x002b TESTQ	AX, AX		    ;; ..is nil
  0x002e JEQ	92		    ;; if it is, jump to 0x005c (panic)

  0x0030 MOVL	(AX), AX            ;; dereference pointer receiver..
  0x0032 MOVL	AX, (SP)            ;; ..and move (i.e. copy) the resulting value to argument #1

  ;; forward (copy) arguments #2 & #3 then call the wrappee
  0x0035 MOVL	"".a+48(SP), AX
  0x0039 MOVL	AX, 4(SP)
  0x003d MOVL	"".b+52(SP), AX
  0x0041 MOVL	AX, 8(SP)
  0x0045 CALL	"".Adder.AddVal(SB) ;; call the wrapped method

  ;; copy return value from wrapped method then return
  0x004a MOVL	16(SP), AX
  0x004e MOVL	AX, "".~r2+56(SP)
  ;; ...omitted frame-pointer stuff...
  0x005b RET

  ;; throw a panic with a detailed error
  0x005c CALL	runtime.panicwrap(SB)

  ;; ...omitted epilogues...
```

显然的，这种包装行为会引入一些成本，因为我们需要将参数拷贝来拷贝去；当原始方法的指令比较少时，这种消耗就是值得考量的了。
幸运的是，实际情况下编译器会将被包装的方法直接内联到包装方法中来避免这些拷贝消耗(在可行的情况下)。

注意符号定义中的 `WRAPPER` 指令，该指令表明这个方法不应该在 backtraces 中出现(避免干扰用户)，也不能从原始方法的 panic 中 recover。

> WRAPPER: This is a wrapper function and should not count as disabling recover.

`runtime.panicwrap` 函数，在包装方法的 receiver 是 `nil` 时会 panic，代码浅显易懂；下面是完整的内容 ([src/runtime/error.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/error.go#L132-L157)):

```Go
// panicwrap generates a panic for a call to a wrapped value method
// with a nil pointer receiver.
//
// It is called from the generated wrapper code.
func panicwrap() {
    pc := getcallerpc()
    name := funcname(findfunc(pc))
    // name is something like "main.(*T).F".
    // We want to extract pkg ("main"), typ ("T"), and meth ("F").
    // Do it by finding the parens.
    i := stringsIndexByte(name, '(')
    if i < 0 {
        throw("panicwrap: no ( in " + name)
    }
    pkg := name[:i-1]
    if i+2 >= len(name) || name[i-1:i+2] != ".(*" {
        throw("panicwrap: unexpected string after package name: " + name)
    }
    name = name[i+2:]
    i = stringsIndexByte(name, ')')
    if i < 0 {
        throw("panicwrap: no ) in " + name)
    }
    if i+2 >= len(name) || name[i:i+2] != ")." {
        throw("panicwrap: unexpected string after type name: " + name)
    }
    typ := name[:i]
    meth := name[i+2:]
    panic(plainError("value method " + pkg + "." + typ + "." + meth + " called using nil *" + typ + " pointer"))
}
```

这些就是所有函数和方法的调用方式了，下面我们来研究主菜: interface。

## Anatomy of an interface

### Overview of the datastructures

开始理解 interface 如何工作之前，我们需要先对组成 interface 的数据结构和其在内存中的布局建立基础的心智模型。
为了达到目的，我们先对 runtime 包里相关的代码做简单的阅览，从 Go 语言实现的角度上来看看 interface 到底长什么样。

**`iface` 结构体**

`iface` 是 runtime 中对 interface 进行表示的根类型 ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L143-L146))。
它的定义长这样:
```Go
type iface struct { // 16 bytes on a 64bit arch
    tab  *itab
    data unsafe.Pointer
}
```

一个 interface 就是这样一个非常简单的结构体，内部维护两个指针:
- `tab` 持有 `itab` 对象的地址，该对象内嵌了描述 interface 类型和其指向的数据类型的数据结构。
- `data` 是一个 raw (i.e. `unsafe`) pointer，指向 interface 持有的具体的值。

虽然很简单，不过数据结构的定义已经提供了重要的信息: 由于 interface 只能持有指针，*任何用 interface 包装的具体类型，都会被取其地址*。
这样多半会导致一次堆上的内存分配，编译器会保守地让 receiver 逃逸。
即使是标量类型，也不例外！

只需要几行代码就可以对上述结论进行证明 ([escape.go](./escape.go)):
```Go
type Addifier interface{ Add(a, b int32) int32 }

type Adder struct{ name string }
//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }

func main() {
    adder := Adder{name: "myAdder"}
    adder.Add(10, 32)	      // doesn't escape
    Addifier(adder).Add(10, 32) // escapes
}
```
```Bash
$ GOOS=linux GOARCH=amd64 go tool compile -m escape.go
escape.go:13:10: Addifier(adder) escapes to heap
# ...
```

这个分配操作还可以直接通过简单的 benchmark 来可视化 ([escape_test.go](./escape_test.go)):
```Go
func BenchmarkDirect(b *testing.B) {
    adder := Adder{id: 6754}
    for i := 0; i < b.N; i++ {
        adder.Add(10, 32)
    }
}

func BenchmarkInterface(b *testing.B) {
    adder := Adder{id: 6754}
    for i := 0; i < b.N; i++ {
        Addifier(adder).Add(10, 32)
    }
}
```
```Bash
$ GOOS=linux GOARCH=amd64 go tool compile -m escape_test.go 
# ...
escape_test.go:22:11: Addifier(adder) escapes to heap
# ...
```
```Bash
$ GOOS=linux GOARCH=amd64 go test -bench=. -benchmem ./escape_test.go
BenchmarkDirect-8      	2000000000	         1.60 ns/op	       0 B/op	       0 allocs/op
BenchmarkInterface-8   	100000000	         15.0 ns/op	       4 B/op	       1 allocs/op
```

能够明显地看到每次我们创建新的 `Addifier` 接口并用 `adder` 变量初始化它时，`sizeof(Addr)` 都会发生一次堆内存分配。
本章晚些时候，我们将会研究简单的标量类型在和 interface 结合时，是如何导致堆内存分配的。

现在先把注意力集中在下一个数据结构上: `itab`。

**`itab` 结构**

`itab` 是这样定义的 ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L648-L658)):
```Go
type itab struct { // 40 bytes on a 64bit arch
    inter *interfacetype
    _type *_type
    hash  uint32 // copy of _type.hash. Used for type switches.
    _     [4]byte
    fun   [1]uintptr // variable sized. fun[0]==0 means _type does not implement inter.
}
```

`itab` 是 interface 的核心。

首先，`itab` 内嵌了 `_type`，`_type` 这个类型是 runtime 对任意 Go 语言类型的内部表示。
`_type` 类型描述了一个“类型”的每一个方面: 类型名字，特性(e.g. 大小，对齐方式...)，某种程度上类型的行为(e.g. 比较，哈希...) 也包含在内了。
在这个例子中，`_type` 字段描述了 interface 所持有的值的类型，`data` 指针所指向的值的类型。

其次，我们找到了一个指向 `interfacetype` 的指针，这只是一个包装了 `_type` 和额外的与 interface 相关的信息的字段。
像你所期望的一样，`inter` 字段描述了 interface 本身的类型。

最后，`func` 数组持有组成该 interface 虚(virtual/dispatch)函数表的的函数的指针。
注意这里的注释中说 `// variable sized` 即“变长”，这表示这里数组所声明的长度是 *非精确*的。
本章我们就会看到，编译器对该数组的空间分配负责，并且其分配操作所用的大小值和这里表示的大小值是不匹配的。同样的，runtime 会始终使用 raw pointer 来访问这段内存，边界检查在这里不会生效。

**`_type` 结构**

如上所述，`_type` 结构对 Go 的类型给出了完成的描述。
其定义在 ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L25-L43)):
```Go
type _type struct { // 48 bytes on a 64bit arch
    size       uintptr
    ptrdata    uintptr // size of memory prefix holding all pointers
    hash       uint32
    tflag      tflag
    align      uint8
    fieldalign uint8
    kind       uint8
    alg        *typeAlg
    // gcdata stores the GC type data for the garbage collector.
    // If the KindGCProg bit is set in kind, gcdata is a GC program.
    // Otherwise it is a ptrmask bitmap. See mbitmap.go for details.
    gcdata    *byte
    str       nameOff
    ptrToThis typeOff
}
```

还好这里大多数的字段名字都做到了自解释。

`nameOff` 和 `typeOff` 类型是 `int32` ，这两个值是链接器负责嵌入的，相对于可执行文件的元信息的偏移量。元信息会在运行期，加载到 `runtime.moduledata` 结构体中 ([src/runtime/symtab.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/symtab.go#L352-L393)), 如果你曾经研究过 ELF 文件的内容的话，看起来会显得很熟悉。
runtime 提供了一些 helper 函数，这些函数能够帮你找到相对于 `moduledata` 的偏移量，比如 `resolveNameOff` ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L168-L196)) and `resolveTypeOff` ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L202-L236)):
```Go
func resolveNameOff(ptrInModule unsafe.Pointer, off nameOff) name {}
func resolveTypeOff(ptrInModule unsafe.Pointer, off typeOff) *_type {}
```
也就是说，假设 `t` 是 `_type` 的话，只要调用 `resolveTypeOff(t, t.ptrToThis) 就可以返回 `t` 的一份拷贝了。

**`interfacetype` 结构体**

最后是 `interfacetype` 结构体 ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L342-L346)):
```Go
type interfacetype struct { // 80 bytes on a 64bit arch
    typ     _type
    pkgpath name
    mhdr    []imethod
}

type imethod struct {
    name nameOff
    ityp typeOff
}
```

像之前提过的，`interfacetype` 只是对于 `_type` 的一种包装，在其顶部空间还包装了额外的 interface 相关的元信息。
在最近的实现中，这部分元信息一般是由一些指向相应名字的 offset 的列表和 interface 所暴露的方法的类型所组成(`[]imethod`)。 

**结论**

下面是对 `iface` 的一份总览，我们把所有的子类型都做了展开；这样应该能够更好地帮助我们融会贯通:
```Go
type iface struct { // `iface`
    tab *struct { // `itab`
        inter *struct { // `interfacetype`
            typ struct { // `_type`
                size       uintptr
                ptrdata    uintptr
                hash       uint32
                tflag      tflag
                align      uint8
                fieldalign uint8
                kind       uint8
                alg        *typeAlg
                gcdata     *byte
                str        nameOff
                ptrToThis  typeOff
            }
            pkgpath name
            mhdr    []struct { // `imethod`
                name nameOff
                ityp typeOff
            }
        }
        _type *struct { // `_type`
            size       uintptr
            ptrdata    uintptr
            hash       uint32
            tflag      tflag
            align      uint8
            fieldalign uint8
            kind       uint8
            alg        *typeAlg
            gcdata     *byte
            str        nameOff
            ptrToThis  typeOff
        }
        hash uint32
        _    [4]byte
        fun  [1]uintptr
    }
    data unsafe.Pointer
}
```

本小节对组成 interface 的不同的数据类型进行了介绍，使我们建立了接口相关知识的心智模型，并帮我们了解了这些部件如何协同工作。
在下一节中，我们将会学习这些数据结构如何辅助计算。

### Creating an interface

我们已经对 interface 的内部数据结构进行了快速学习，接下来主要聚焦在他们如何被分配以及如何初始化。

看一下下面的程序 ([iface.go](./iface.go)):
```Go
type Mather interface {
    Add(a, b int32) int32
    Sub(a, b int64) int64
}

type Adder struct{ id int32 }
//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }
//go:noinline
func (adder Adder) Sub(a, b int64) int64 { return a - b }

func main() {
    m := Mather(Adder{id: 6754})

    // This call just makes sure that the interface is actually used.
    // Without this call, the linker would see that the interface defined above
    // is in fact never used, and thus would optimize it out of the final
    // executable.
    m.Add(10, 32)
}
```

*NOTE: 本章的剩余部分，我们演示一个持有 `T` 类型内容 `I` 类型的 interface，即 `<I,T>`。比如这里的 `Mather(Adder{id: 6754})` 就实例化了一个 `iface<Mather, Adder>`。

主要聚焦在 `iface<Mather, Adder>` 的实例化:
```Go
m := Mather(Adder{id: 6754})
```
这一行代码内就完成了很多机关，编译器生成的汇编可以证明:
```Assembly
;; part 1: allocate the receiver
0x001d MOVL	$6754, ""..autotmp_1+36(SP)
;; part 2: set up the itab
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX
0x002c MOVQ	AX, (SP)
;; part 3: set up the data
0x0030 LEAQ	""..autotmp_1+36(SP), AX
0x0035 MOVQ	AX, 8(SP)
0x003a CALL	runtime.convT2I32(SB)
0x003f MOVQ	16(SP), AX
0x0044 MOVQ	24(SP), CX
```

像你所看到的，我们将输出划分成了三个逻辑部分。

**Part 1: 分配 receiver 的空间**

```Assembly
0x001d MOVL	$6754, ""..autotmp_1+36(SP)
```

十进制常量 `6754` 对应的是我们 `Adder` 的 ID，被存储在当前栈帧的起始位置。
之后编译器就可以根据它的存储位置来用地址对其进行引用了；我们会在 part 3 中看到原因。

**Part 2: 创建 itab**

```Assembly
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX
0x002c MOVQ	AX, (SP)
```

看起来编译器已经创建了必要的 `itab` 来表示我们的 `iface<Mather, Adder>` interface，并以全局符号 `go.itab."".Adder,"".Mather` 提供给我们使用。

我们正在执行创建 `iface<Mather, Adder>` interface 的流程中，为了能够完成工作，我们将该全局变量 `go.itab."".Adder,"".Mather` 的地址使用 LEAQ 指令从栈帧顶 load 到 AX 寄存器。
这段行为的原因我们也会在 part 3 中解释。

文法上，我们可以用下面这行伪代码来代替上面的几行代码:
```Go
tab := getSymAddr(`go.itab.main.Adder,main.Mather`).(*itab)
```
到此已经完成了我们 interface 的一半工作了。

我们来更深入地研究一下 `go.itab."".Adder,"".Mather` 这个符号。
像往常一样，编译器的 `-S` flag 已经告诉了我们很多信息:
```
$ GOOS=linux GOARCH=amd64 go tool compile -S iface.go | grep -A 7 '^go.itab."".Adder,"".Mather'
go.itab."".Adder,"".Mather SRODATA dupok size=40
    0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
    0x0010 8a 3d 5f 61 00 00 00 00 00 00 00 00 00 00 00 00  .=_a............
    0x0020 00 00 00 00 00 00 00 00                          ........
    rel 0+8 t=1 type."".Mather+0
    rel 8+8 t=1 type."".Adder+0
    rel 24+8 t=1 "".(*Adder).Add+0
    rel 32+8 t=1 "".(*Adder).Sub+0
```

代码很整洁。我们来一句一句地分析一下。

第一句声明了符号和它的属性:
```
go.itab."".Adder,"".Mather SRODATA dupok size=40
```
和通常一样，由于我们看的是编译器生成的间接目标文件(i.e. 即链接器还没有运行)，符号名还没有把 package 名字填充上。其它的没啥新东西。
除此之外，我们这里得到的是一个 40-字节的全局对象的符号，该符号将被存到二进制文件的 `.rodata` 段中。

注意这里的 `dupok` 指令，这个指令会告诉链接器如果这个符号在链接期出现多次的话是 ok 的: 链接器将随意选择其中的一个。
是什么让 Go 的作者们认为这个符号会出现重复，我不是很清楚。如果你了解更多细节的话，欢迎开一个 issue 来讨论。

下面这段代码是和该符号相关的 hexdump 的 40 个字节的数据。也就是说，这是一个 `itab`  结构体被序列化之后的表示方法。
```
0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
0x0010 8a 3d 5f 61 00 00 00 00 00 00 00 00 00 00 00 00  .=_a............
0x0020 00 00 00 00 00 00 00 00                          ........
```
如你所见，这部分数据大部分是由 0 组成的。链接器会负责填充这些 0，我们马上就会看到这些是怎么完成的。

注意在这些 0 中，在 offset `0x10+4` 的位置，有 4 个字节的值被设置过了。
回忆一下 `itab` 结构体的声明，并在在对应的字段上打上注释:

```Go
type itab struct { // 40 bytes on a 64bit arch
    inter *interfacetype // offset 0x00 ($00)
    _type *_type	 // offset 0x08 ($08)
    hash  uint32	 // offset 0x10 ($16)
    _     [4]byte	 // offset 0x14 ($20)
    fun   [1]uintptr	 // offset 0x18 ($24)
			 // offset 0x20 ($32)
}
```
可以看到 offset `0x10+4` 和 `hash uint32` 字段是匹配的: 也就是说，对应 `main.Adder` 类型的 hash 值已经在我们的目标文件中了。

第三即最后一部分列出了提供给链接器的重定向指令:
```
rel 0+8 t=1 type."".Mather+0
rel 8+8 t=1 type."".Adder+0
rel 24+8 t=1 "".(*Adder).Add+0
rel 32+8 t=1 "".(*Adder).Sub+0
```

`rel 0+8 t=1 type."".Mather+0` 告诉链接器需要将内容的前 8 个字节(`0+8`) 填充为全局目标符号 `type."".Mather` 的地址。
`rel 8+8 t=1 type."".Adder+0` 然后用 `type."".Adder` 的地址填充接下来的 8 个字节，之后类似。

一旦链接器完成了它的工作，执行完了这些指令，40-字节的序列化后的 `itab` 就完成了。
总体来讲，我们在看的代码类似下面这些伪代码:
```Go
tab := getSymAddr(`go.itab.main.Adder,main.Mather`).(*itab)

// NOTE: The linker strips the `type.` prefix from these symbols when building
// the executable, so the final symbol names in the .rodata section of the
// binary will actually be `main.Mather` and `main.Adder` rather than
// `type.main.Mather` and `type.main.Adder`.
// Don't get tripped up by this when toying around with objdump.
tab.inter = getSymAddr(`type.main.Mather`).(*interfacetype)
tab._type = getSymAddr(`type.main.Adder`).(*_type)

tab.fun[0] = getSymAddr(`main.(*Adder).Add`).(uintptr)
tab.fun[1] = getSymAddr(`main.(*Adder).Sub`).(uintptr)
```

我们已经得到了一个完整可用的 `itab`，如果能再有一些相关的数据塞进去，就能得到一个完整的更好的 interface 了。

**Part 3: 分配数据**

```Assembly
0x0030 LEAQ	""..autotmp_1+36(SP), AX
0x0035 MOVQ	AX, 8(SP)
0x003a CALL	runtime.convT2I32(SB)
0x003f MOVQ	16(SP), AX
0x0044 MOVQ	24(SP), CX
```

在 part 1 我们说过，现在栈顶`(SP)` 保存着 `go.itab."".Adder."".Mather` 的地址(参数 #1)。
同时 part 2 我们在 `""..autotmp_1+36(SP)` 位置存储了一个十进制常量 `$6754`: 我们用 8(SP) 来将该栈顶下方的该变量(参数 #2) load 到寄存器中。

这两个指针是我们传给 `runtime.convT2I32` 函数的两个参数，该函数将最后的步骤粘起来，创建并返回我们完整的 interface。
我们再仔细看一下 ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L433-L451)):
```Go
func convT2I32(tab *itab, elem unsafe.Pointer) (i iface) {
    t := tab._type
    /* ...omitted debug stuff... */
    var x unsafe.Pointer
    if *(*uint32)(elem) == 0 {
        x = unsafe.Pointer(&zeroVal[0])
    } else {
        x = mallocgc(4, t, false)
        *(*uint32)(x) = *(*uint32)(elem)
    }
    i.tab = tab
    i.data = x
    return
}
```

所以 `runtime.convT2I32` 做了 4 件事情:
1. 它创建了一个 `iface` 的结构体 `i` (这里学究一点的话，是它的 caller 创建的这个结构体..没啥两样)。
2. 它将我们刚给 `i.tab` 赋的值赋予了 `itab` 指针。
3. 它 **在堆上分配了一个 `i.tab._type` 的新对象 `i.tab._type`**，然后将第二个参数 `elem` 指向的值拷贝到这个新对象上。
4. 将最后的 interface 返回。

这个过程比较直截了当，尽管第三步在这种特定 case 下包含了一些 tricky 的实现细节，这些麻烦的细节是因为我们的 `Adder` 是一个标量类型。
我们会在 [the special cases of interfaces](#interface-holding-a-scalar-type) 一节来研究标量类型和 interface 交互的更多细节。

现在我们已经完成了下面这些工作(伪代码):
```Go
tab := getSymAddr(`go.itab.main.Adder,main.Mather`).(*itab)
elem := getSymAddr(`""..autotmp_1+36(SP)`).(*int32)

i := runtime.convTI32(tab, unsafe.Pointer(elem))

assert(i.tab == tab)
assert(*(*int32)(i.data) == 6754) // same value..
assert((*int32)(i.data) != elem)  // ..but different (al)locations!
```

总结一下目前所有的内容，这里是一份完整带注释的汇编代码，包含了所有 3 个部分:
```Assembly
0x001d MOVL	$6754, ""..autotmp_1+36(SP)         ;; create an addressable $6754 value at 36(SP)
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX  ;; set up go.itab."".Adder,"".Mather..
0x002c MOVQ	AX, (SP)                            ;; ..as first argument (tab *itab)
0x0030 LEAQ	""..autotmp_1+36(SP), AX            ;; set up &36(SP)..
0x0035 MOVQ	AX, 8(SP)                           ;; ..as second argument (elem unsafe.Pointer)
0x003a CALL	runtime.convT2I32(SB)               ;; call convT2I32(go.itab."".Adder,"".Mather, &$6754)
0x003f MOVQ	16(SP), AX                          ;; AX now holds i.tab (go.itab."".Adder,"".Mather)
0x0044 MOVQ	24(SP), CX                          ;; CX now holds i.data (&$6754, somewhere on the heap)
```
记住，这些代码都是 `m := Mather(Adder{id: 6754})` 这一行代码生成的。

最终，我们得到了完整的，可以工作的 interface。

### Reconstructing an `itab` from an executable

前一节中，我们从编译器生成的目标文件中 dump 出了 `go.itab."".Adder,"".Mather`，并看到在一串 0 中出现了 hash 值:
```
$ GOOS=linux GOARCH=amd64 go tool compile -S iface.go | grep -A 3 '^go.itab."".Adder,"".Mather'
go.itab."".Adder,"".Mather SRODATA dupok size=40
    0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
    0x0010 8a 3d 5f 61 00 00 00 00 00 00 00 00 00 00 00 00  .=_a............
    0x0020 00 00 00 00 00 00 00 00                          ........
```

为了能更好地理解链接器如何分配这些数据的地址，我们会研究生成的 ELF 文件，并手动重建组成我们的 `iface<Mather, Adder>` 的 `itab` 的数据。

这样可以让我们观察 `itab` 在链接器完成工作之后又是长什么样子的。

先来最重要的事情，我们来把 `iface` 编译好: `GOOS=linux GOARCH=amd64 go build -o iface.bin iface.go`。

**Step 1: Find `.rodata`**

我们来打印一下 section 头，以研究 `.rodata` 部分，`readelf` 这个工具可以很方便的完成这件事:
```Bash
$ readelf -St -W iface.bin
There are 22 section headers, starting at offset 0x190:

Section Headers:
  [Nr] Name
       Type            Address          Off    Size   ES   Lk Inf Al
       Flags
  [ 0] 
       NULL            0000000000000000 000000 000000 00   0   0  0
       [0000000000000000]: 
  [ 1] .text
       PROGBITS        0000000000401000 001000 04b3cf 00   0   0 16
       [0000000000000006]: ALLOC, EXEC
  [ 2] .rodata
       PROGBITS        000000000044d000 04d000 028ac4 00   0   0 32
       [0000000000000002]: ALLOC
## ...omitted rest of output...
```
我们现在需要的是 section 中十进制的 offset 值，所以结合 linux 的 pipe 来组合一些命令: 
```Bash
$ readelf -St -W iface.bin | \
  grep -A 1 .rodata | \
  tail -n +2 | \
  awk '{print "ibase=16;"toupper($3)}' | \
  bc
315392
```

这个输出表示 `fseek` 了 315392 个字节才能让我们定位到 `.rodata` section。
这下我们就只需要将文件文中 map 到虚拟内存地址中了。

**Step 2: Find the virtual-memory address (VMA) of `.rodata`**

VMA 是当我们的二进制文件被 OS load 到内存时，section 被 map 到的虚拟地址。也就是说，这是我们在运行时引用的符号的具体地址。

我们关注 VMA，是因为我们没法通过调用 `readelf` 或者 `objdump` 来找到想要的符号的 offset(就我所知)。另一方面，我们想知道的是运行时的符号的虚拟地址。
只要再进行一些简单的数学运算，我们应该就可以在 VMA 和 offset 之间建立联系，并最终找到我们想要的符号偏移量了。

找到 `.rodata` 的 VMA 和寻找它的 offset 没啥区别，只有一列有区别:
```Bash
$ readelf -St -W iface.bin | \
  grep -A 1 .rodata | \
  tail -n +2 | \
  awk '{print "ibase=16;"toupper($2)}' | \
  bc
4509696
```

我们已知的信息: `.rodata` 段的偏移量是 ELF 文件中的 `$315392`(= `0x04d000`) 位置，该位置会在运行期被映射到虚拟地址 `$4509696`(=`0x44d000`)。

现在我们需要正在定位的符号的 VMA 和符号的大小:
- VMA 将(间接)允许我们在可执行文件中间接进行定位。
- 其大小将让我们知道找到了 offset 之后，需要读多少个字节的数据就能把其数据读出来。

**Step 3: Find the VMA & size of `go.itab.main.Adder,main.Mather`**

`objdump` 对我们有下面这些用处。

首先，找到符号:
```Bash
$ objdump -t -j .rodata iface.bin | grep "go.itab.main.Adder,main.Mather"
0000000000475140 g     O .rodata	0000000000000028 go.itab.main.Adder,main.Mather
```

然后获取到它的十进制形式的虚拟地址:
```Bash
$ objdump -t -j .rodata iface.bin | \
  grep "go.itab.main.Adder,main.Mather" | \
  awk '{print "ibase=16;"toupper($1)}' | \
  bc
4673856
```

最后获取到十进制的符号大小:
```Bash
$ objdump -t -j .rodata iface.bin | \
  grep "go.itab.main.Adder,main.Mather" | \
  awk '{print "ibase=16;"toupper($5)}' | \
  bc
40
```

所以 `go.itab.main.Adder,main.Mather` 运行时会被映射到 `$4673856`(=`0x475140`) 这个虚拟地址，并且其大小为 40 个字节(我们之前也知道了，这个就是 `itab` 数据结构的大小)

**Step 4: Find & extract `go.itab.main.Adder,main.Mather`**
现在我们有了研究二进制文件中 `go.itab.main.Adder,main.Mather` 这个符号所需要的全部要素。

下面是对我们已知信息的备忘:
```
.rodata offset: 0x04d000 == $315392
.rodata VMA: 0x44d000 == $4509696

go.itab.main.Adder,main.Mather VMA: 0x475140 == $4673856
go.itab.main.Adder,main.Mather size: 0x24 = $40
```

如果 `$315392` (`.rodata` 的 offset) 映射到 $4509696 (`.rodata` 的 VMA) 并且 `go.itab.main.Adder,main.Mather` 的 VMA 是 `$4673856`， 然后 `go.itab.main.Adder,main.Mather` 在可执行文件中的的 offset 是:  
`sym.offset = sym.vma - section.vma + section.offset = $4673856 - $4509696 + $315392 = $479552`.

因为我们已经知道了 offset 和数据的大小，我们可以掏出我们的好伙伴 `dd` 来将这些原始字节直接从可执行文件中搞出来:
```Bash
$ dd if=iface.bin of=/dev/stdout bs=1 count=40 skip=479552 2>/dev/null | hexdump
0000000 bd20 0045 0000 0000 ed40 0045 0000 0000
0000010 3d8a 615f 0000 0000 c2d0 0044 0000 0000
0000020 c350 0044 0000 0000                    
0000028
```

看起来我们获得了显著的胜利。。不过是真的胜利了么？也许我们只是 dump 出了 40 个随机的字节呢，和我们想要的数据根本无关呢？谁知道呢？
有一个办法能帮我们确认这件事: 让我们将二进制 dump(offset `0x10+4` -> `0x615f3d8a`) 的 type hash 和 runtime 加载的 ([iface_type_hash.go](./iface_type_hash.go)) 进行对比:
```Go
// simplified definitions of runtime's iface & itab types
type iface struct {
    tab  *itab
    data unsafe.Pointer
}
type itab struct {
    inter uintptr
    _type uintptr
    hash  uint32
    _     [4]byte
    fun   [1]uintptr
}

func main() {
    m := Mather(Adder{id: 6754})

    iface := (*iface)(unsafe.Pointer(&m))
    fmt.Printf("iface.tab.hash = %#x\n", iface.tab.hash) // 0x615f3d8a
}
```

匹配上了！`fmt.Printf("iface.tab.hash = %#x\n",iface.tab.hash)` 给了我们 `0x615f3d8a` 这个结果，和我们从 ELF 文件了扒出来的内容是一致的。

**结论**

我们为 `iface<Mather, Adder>` 接口重建了完整的 `itab` 结构；这个结构就躺在我们的可执行文件里等待被使用，并且已经有了 runtime 使 interface 按照需求工作所需要的一切信息。

当然，因为 `itab` 大多数时候由一堆指向其它数据结构的指针构成，我们还需要跟踪用 `dd` 扒出来的内容中的虚拟地址才能重建出整个运行图。

提到指针的话，我们现在对 `iface<Mather, Adder>` 的虚表已经有了清晰的认识；这里是 `go.itab.main.Adder,main.Mather` 内容的一份注解版本:
```Bash
$ dd if=iface.bin of=/dev/stdout bs=1 count=40 skip=479552 2>/dev/null | hexdump
0000000 bd20 0045 0000 0000 ed40 0045 0000 0000
0000010 3d8a 615f 0000 0000 c2d0 0044 0000 0000
#                           ^^^^^^^^^^^^^^^^^^^
#                       offset 0x18+8: itab.fun[0]
0000020 c350 0044 0000 0000                    
#       ^^^^^^^^^^^^^^^^^^^
# offset 0x20+8: itab.fun[1]
0000028
```
```Bash
$ objdump -t -j .text iface.bin | grep 000000000044c2d0
000000000044c2d0 g     F .text	0000000000000079 main.(*Adder).Add
```
```Bash
$ objdump -t -j .text iface.bin | grep 000000000044c350
000000000044c350 g     F .text	000000000000007f main.(*Adder).Sub
```

毫无意外，`iface<Mather, Adder>` 的虚表持有了两个方法指针: `main.(*Adder).add` 和 `main.(*Adder).sub`。
好吧，这里*确实*有一点奇怪: 我们从来没有定义过有指针 receiver 的这两个方法。
编译器代表我们直接生成了这些包装方法(如之前在 ["Implicit dereferencing" section](#implicit-dereferencing) 中描述的)，因为它知道我们会需要这些方法: 因为我们的 `Adder` 实现中只提供了值-receiver 的方法，如果某个时刻，我们通过虚表调用任何一个 interface 中的方法，都会需要这里的包装方法。

这里应该已经让你对动态 dispatch 在运行期间如何处理，有了初步的不错理解；下一节我们会研究这个问题。

**Bonus**

我写了一个通用的 bash 脚本，可以直接用来 dump 出 ELF 文件中的任何段的任何符号的内容 ([dump_sym.sh](./dump_sym.sh)):
```Bash
# ./dump_sym.sh bin_path section_name sym_name
$ ./dump_sym.sh iface.bin .rodata go.itab.main.Adder,main.Mather
.rodata file-offset: 315392
.rodata VMA: 4509696
go.itab.main.Adder,main.Mather VMA: 4673856
go.itab.main.Adder,main.Mather SIZE: 40

0000000 bd20 0045 0000 0000 ed40 0045 0000 0000
0000010 3d8a 615f 0000 0000 c2d0 0044 0000 0000
0000020 c350 0044 0000 0000
0000028
```

按说应该哪里是有什么工具可以提供这个脚本的功能的，可能只要给 `binutils` 里的某个工具传一些诡异的 flag 就可以拿到这些内容。。谁知道呢。
如果你知道已经有工具提供了这个功能的话，不要犹豫，开 issue 告诉我。

## Dynamic dispatch

本节我们终于要讲 interface 最主要的功能了: 动态分发。
明确一些，我们主要研究动态分发在底层如何工作，并且使用动态分发有什么样的成本。

### Indirect method call on interface

再回看一下最初的代码 ([iface.go](./iface.go)):
```Go
type Mather interface {
    Add(a, b int32) int32
    Sub(a, b int64) int64
}

type Adder struct{ id int32 }
//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }
//go:noinline
func (adder Adder) Sub(a, b int64) int64 { return a - b }

func main() {
    m := Mather(Adder{id: 6754})
    m.Add(10, 32)
}
```
我们对这些代码背后所发生的事情已经有了深入的理解: `iface<Mather, Adder>` interface 如何创建，在可执行文件中如何布局，最终如何被 runtime 加载。
之后就只剩一件事情还需要琢磨，就是对 `m.Add(10, 32)` 的间接调用会做些什么事情了。

为了让我们能够回忆起之前学到的内容，我们会同时关注 interface 的创建和方法的调用过程:
```Go
m := Mather(Adder{id: 6754})
m.Add(10, 32)
```
还好我们已经有了第一行实例化 (`m := Mather(Adder{id: 6754})`) 时完整带注释的汇编代码:
```Assembly
;; m := Mather(Adder{id: 6754})
0x001d MOVL	$6754, ""..autotmp_1+36(SP)         ;; create an addressable $6754 value at 36(SP)
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX  ;; set up go.itab."".Adder,"".Mather..
0x002c MOVQ	AX, (SP)                            ;; ..as first argument (tab *itab)
0x0030 LEAQ	""..autotmp_1+36(SP), AX            ;; set up &36(SP)..
0x0035 MOVQ	AX, 8(SP)                           ;; ..as second argument (elem unsafe.Pointer)
0x003a CALL	runtime.convT2I32(SB)               ;; runtime.convT2I32(go.itab."".Adder,"".Mather, &$6754)
0x003f MOVQ	16(SP), AX                          ;; AX now holds i.tab (go.itab."".Adder,"".Mather)
0x0044 MOVQ	24(SP), CX                          ;; CX now holds i.data (&$6754, somewhere on the heap)
```
接着是对方法间接调用 (`m.Add(10, 32)`)的汇编代码:
```Assembly
;; m.Add(10, 32)
0x0049 MOVQ	24(AX), AX
0x004d MOVQ	$137438953482, DX
0x0057 MOVQ	DX, 8(SP)
0x005c MOVQ	CX, (SP)
0x0060 CALL	AX
```

有了之前几小节积累的知识，这些指令对我们来说就很直白了。

```Assembly
0x0049 MOVQ	24(AX), AX
```
`runtime.convT2I32` 一返回，`AX` 中就包含了 `i.tab` 的指针；更准确地说是指向 `go.itab."".Adder."".Mather` 的指针。
将 `AX` 解引用，然后向前 offset 24 个字节，我们就可以找到 `i.tab.fun` 的位置了，这个地址对应的是虚表的第一个入口。
下面的代码帮我们回忆一下 `itab` 长啥样:

```Go
type itab struct { // 32 bytes on a 64bit arch
    inter *interfacetype // offset 0x00 ($00)
    _type *_type	 // offset 0x08 ($08)
    hash  uint32	 // offset 0x10 ($16)
    _     [4]byte	 // offset 0x14 ($20)
    fun   [1]uintptr	 // offset 0x18 ($24)
			 // offset 0x20 ($32)
}
```

之前小节中我们通过从可执行文件中重建 `itab` 结构，已经知道了 `iface.tab.fun[0]` 是指向 `main.(*Adder).add` 的指针，这是编译器生成的包装方法，该方法会继而调用我们原始的 `main.Adder.add` 方法。

```Assembly
0x004d MOVQ	$137438953482, DX
0x0057 MOVQ	DX, 8(SP)
```
将 `10`  和 `32` 作为参数 #2 和 #3 存在栈顶。

```Assembly
0x005c MOVQ	CX, (SP)
0x0060 CALL	AX
```
`runtime.convT2I32` 一返回， `CX` 寄存器就存了 `i.data`，该指针指向 `Adder` 实例。
我们将该指针移动到栈顶，作为参数 #1，为了能够满足调用规约: receiver 必须作为方法的第一个参数传入。

最后，栈建好了，可以执行函数调用了。

这里给出完整流程的带注释的汇编代码，作为本节的收尾。
```Assembly
;; m := Mather(Adder{id: 6754})
0x001d MOVL	$6754, ""..autotmp_1+36(SP)         ;; create an addressable $6754 value at 36(SP)
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX  ;; set up go.itab."".Adder,"".Mather..
0x002c MOVQ	AX, (SP)                            ;; ..as first argument (tab *itab)
0x0030 LEAQ	""..autotmp_1+36(SP), AX            ;; set up &36(SP)..
0x0035 MOVQ	AX, 8(SP)                           ;; ..as second argument (elem unsafe.Pointer)
0x003a CALL	runtime.convT2I32(SB)               ;; runtime.convT2I32(go.itab."".Adder,"".Mather, &$6754)
0x003f MOVQ	16(SP), AX                          ;; AX now holds i.tab (go.itab."".Adder,"".Mather)
0x0044 MOVQ	24(SP), CX                          ;; CX now holds i.data (&$6754, somewhere on the heap)
;; m.Add(10, 32)
0x0049 MOVQ	24(AX), AX                          ;; AX now holds (*iface.tab)+0x18, i.e. iface.tab.fun[0]
0x004d MOVQ	$137438953482, DX                   ;; move (32,10) to..
0x0057 MOVQ	DX, 8(SP)                           ;; ..the top of the stack (arguments #3 & #2)
0x005c MOVQ	CX, (SP)                            ;; CX, which holds &$6754 (i.e., our receiver), gets moved to
                                                    ;; ..the top of stack (argument #1 -> receiver)
0x0060 CALL	AX                                  ;; you know the drill
```

我们对 interface 和虚表的工作所需的所有手段都有了清晰的理解。
下一节，将会分别从理论和实践角度，对 interface 的使用成本进行评估。

### Overhead

如我们所见，interface 代理的实现主要是由编译器和链接器组合完成的。从性能的角度来讲，这种行为显然是好消息: runtime 干的活越少越好。
但还是有一些极端 case，实例化 interface 也需要 runtime 也参与进来(e.g. `runtime.convT2*` 族的函数)，尽管实践上这些函数比较少出现。
在 [section dedicated to the special cases of interfaces](#special-cases--compiler-tricks) 中我们会知道更多的边缘 case。
现在我们还是只聚焦在虚方法的调用成本上，忽略掉初始化的那些一次性成本。

一旦 interface 被正确地实例化了，调用这个 interface 的方法相比于调用静态分发的方法，就只不过是多走一个间接层的问题了(i.e. 解引用 `itab.fun` 数组中对应索引位置的指针)。
因此，可以假设这个过程基本上没啥消耗。。这种假设是对的，但也不完全对: 理论稍微有一些 tricky，事实还更加 tricky 一些。

#### The theory: quick refresher on modern CPUs

虚函数调用的这种间接性在*只要从 CPU 的角度来讲是可以预测的话*，其成本就是可以忽略不计的。
现代 CPU 都是非常激进的怪兽: 他们会非常激进地缓存，激进地对指令和数据进行预取，激进地对代码进行预执行，甚至会在可能的时候将这些指令并行化。
无论我们是否想要，这些额外的工作都会被完成，因此我们应该不要让自己的程序和 CPU 在这方面的优化背道而驰，以免使 CPU 已经运行过的周期都白白浪费。

这就是虚方法调用很快变成麻烦问题的地方。

在静态调用的情况下，CPU 实际上可以提前知道即将执行的程序分支，并根据预测将这些分支的指令提前取到。这样能够最大化地利用 CPU 流水线来将程序的分支都提前执行掉。
而在动态分发的情况下，CPU 没有办法提前知道程序会向哪个方向执行: 因为 interface 的特性，不到运行期，没有办法知道到底要取谁的计算结果。为了平衡这一点，CPU 使用了各种各样的探索和算法以猜出程序即将执行的到底是哪一个分支(i.e. 分支预测)。

如果处理器猜中了，我们就可以期望动态分发的效率和静态分发差不多，由于执行位置的指令已经都被提前取进了处理器的缓存。

如果处理器猜错了的话，就比较麻烦了: 首先，我们需要额外的指令，还需要从主存中加载数据(这会使 CPU 完全失速)到 L1i 缓存中。也可能更糟糕，我们需要付出 CPU 因自身的错误预测而 flush 掉它的指令流水线的成本。
动态分发的另一个缺点是其会使内联从定义上就完全不可能实现了: 都不知道要运行什么东西，自然没有办法内联了。

总而言之，直接调用内联函数 F 和调用有额外的中间层的这些间接调用，在性能方面，理论上会有很大的差距，甚至还可能触发 CPU 的分支误判。

这就是从理论上分析出的可能性。讨论现代硬件的话，我们需要知晓上述理论。

让我们来衡量一下这部分成本。

#### The practice: benchmarks

我们运行 benchmark 的 CPU 信息:
```Bash
$ lscpu | sed -nr '/Model name/ s/.*:\s*(.* @ .*)/\1/p'
Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz
```

我们把要进行 benchmark 的 interface 定义成这样 ([iface_bench_test.go](./iface_bench_test.go)):
```Go
type identifier interface {
    idInline() int32
    idNoInline() int32
}

type id32 struct{ id int32 }

// NOTE: Use pointer receivers so we don't measure the extra overhead incurred by
// autogenerated wrappers as part of our results.

func (id *id32) idInline() int32 { return id.id }
//go:noinline
func (id *id32) idNoInline() int32 { return id.id }
```

**Benchmark suite A: 单实例，多次调用，内联 & 非内联**

我们开头的两个 benchmark 会尝试在 busy-loop 中调用非内联方法，一个是 `*Adder` 值，另一个是 `iface<Mather, *Adder>` 的 interface:
```Go
var escapeMePlease *id32
// escapeToHeap makes sure that `id` escapes to the heap.
//
// In simple situations such as some of the benchmarks present in this file,
// the compiler is able to statically infer the underlying type of the
// interface (or rather the type of the data it points to, to be pedantic) and
// ends up replacing what should have been a dynamic method call by a
// static call.
// This anti-optimization prevents this extra cleverness.
//
//go:noinline
func escapeToHeap(id *id32) identifier {
    escapeMePlease = id
    return escapeMePlease
}

var myID int32

func BenchmarkMethodCall_direct(b *testing.B) {
    b.Run("single/noinline", func(b *testing.B) {
        m := escapeToHeap(&id32{id: 6754}).(*id32)
        for i := 0; i < b.N; i++ {
            // CALL "".(*id32).idNoInline(SB)
            // MOVL 8(SP), AX
            // MOVQ "".&myID+40(SP), CX
            // MOVL AX, (CX)
            myID = m.idNoInline()
        }
    })
}

func BenchmarkMethodCall_interface(b *testing.B) {
    b.Run("single/noinline", func(b *testing.B) {
        m := escapeToHeap(&id32{id: 6754})
        for i := 0; i < b.N; i++ {
            // MOVQ 32(AX), CX
            // MOVQ "".m.data+40(SP), DX
            // MOVQ DX, (SP)
            // CALL CX
            // MOVL 8(SP), AX
            // MOVQ "".&myID+48(SP), CX
            // MOVL AX, (CX)
            myID = m.idNoInline()
        }
    })
}
```

我们期望的结果是，两个 benchmark 在跑 A) 的时候极其快，B) 的速度也差不多。

考虑到 loop 的紧密性，我们可以期望这两个 benchmark 的循环在每次迭代时，都保证其数据(receiver 和虚函数表)和指令(`"".(*id32).idNoInline`)已经在 CPU 的 L1d/L1i 的缓存中了。也就是说，这里的性能可以认为是 CPU-bound。

`BenchmarkMethodCall_interface` 会稍微慢一些(在纳秒级别的评价范围)，因为其需要从虚表(已经在 L1 cache 了)查找并拷贝正确的指针，有一些成本。
由于 `CALL CX` 指令对这些额外指令的输出有强依赖，这些指令用来查虚表，处理器没辙，只能把这些逻辑都当作线性的流来处理，而没法对虚表相关的内容进行指令级的并行。

这是我们会觉得 "interface" 版本运行稍慢的主要原因。

下面是 "直接" 调用版本的结果:
```Bash
$ go test -run=NONE -o iface_bench_test.bin iface_bench_test.go && \
  perf stat --cpu=1 \
  taskset 2 \
  ./iface_bench_test.bin -test.cpu=1 -test.benchtime=1s -test.count=3 \
      -test.bench='BenchmarkMethodCall_direct/single/noinline'
BenchmarkMethodCall_direct/single/noinline         	2000000000	         1.81 ns/op
BenchmarkMethodCall_direct/single/noinline         	2000000000	         1.80 ns/op
BenchmarkMethodCall_direct/single/noinline         	2000000000	         1.80 ns/op

 Performance counter stats for 'CPU(s) 1':

      11702.303843      cpu-clock (msec)          #    1.000 CPUs utilized          
             2,481      context-switches          #    0.212 K/sec                  
                 1      cpu-migrations            #    0.000 K/sec                  
             7,349      page-faults               #    0.628 K/sec                  
    43,726,491,825      cycles                    #    3.737 GHz                    
   110,979,100,648      instructions              #    2.54  insn per cycle         
    19,646,440,556      branches                  # 1678.852 M/sec                  
           566,424      branch-misses             #    0.00% of all branches        

      11.702332281 seconds time elapsed
```
下面是 "interface" 的版本:
```Bash
$ go test -run=NONE -o iface_bench_test.bin iface_bench_test.go && \
  perf stat --cpu=1 \
  taskset 2 \
  ./iface_bench_test.bin -test.cpu=1 -test.benchtime=1s -test.count=3 \
      -test.bench='BenchmarkMethodCall_interface/single/noinline'
BenchmarkMethodCall_interface/single/noinline         	2000000000	         1.95 ns/op
BenchmarkMethodCall_interface/single/noinline         	2000000000	         1.96 ns/op
BenchmarkMethodCall_interface/single/noinline         	2000000000	         1.96 ns/op

 Performance counter stats for 'CPU(s) 1':

      12709.383862      cpu-clock (msec)          #    1.000 CPUs utilized          
             3,003      context-switches          #    0.236 K/sec                  
                 1      cpu-migrations            #    0.000 K/sec                  
            10,524      page-faults               #    0.828 K/sec                  
    47,301,533,147      cycles                    #    3.722 GHz                    
   124,467,105,161      instructions              #    2.63  insn per cycle         
    19,878,711,448      branches                  # 1564.097 M/sec                  
           761,899      branch-misses             #    0.00% of all branches        

      12.709412950 seconds time elapsed
```

结果与我们的期望是相符的: "interface" 版本确实稍慢一些，每个迭代慢 0.15 纳秒，或者说慢了 ~8%。
8% 一开始听着还挺吓人，但我们需要知道 A) 这个 benchmark 是纳秒级的评估，并且 B) 这个被调用的方法除了被调用之外没有做任何实质性的工作，从而夸大了这个差距。

观察一下两个 benchmark 的指令数目，我们可以看到基于 interface 的版本比 "直接" 调用的版本多了 ~140 亿条指令(`110,979,100,648` vs. `124,467,105,161`)，即使 benchmark 本身只运行了 `6,000,000,000` (`2,000,000,000\*3`) 次迭代。 
我们之前提过，CPU 没有办法让这些指令并行化，因为 `CALL` 依赖这些指令，这一点在 “每周期指令比例” 上得到了充分的反映: 两个 benchmark 都得到了相似的 IPC(instruction-per-cycle) 比例，虽然 interface 版本整体上需要干更多的活儿。

缺乏并行的结果最终堆积结果就是造成了 interface 版本的额外的 ~35 亿 CPU 循环周期，这也是这额外的 0.15ns 具体消耗在的地方。

如果我们让编译器把这个方法调用内联的话，会发生什么呢？

```Go
var myID int32

func BenchmarkMethodCall_direct(b *testing.B) {
    b.Run("single/inline", func(b *testing.B) {
        m := escapeToHeap(&id32{id: 6754}).(*id32)
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
            // MOVL (DX), SI
            // MOVL SI, (CX)
            myID = m.idInline()
        }
    })
}

func BenchmarkMethodCall_interface(b *testing.B) {
    b.Run("single/inline", func(b *testing.B) {
        m := escapeToHeap(&id32{id: 6754})
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
            // MOVQ 32(AX), CX
            // MOVQ "".m.data+40(SP), DX
            // MOVQ DX, (SP)
            // CALL CX
            // MOVL 8(SP), AX
            // MOVQ "".&myID+48(SP), CX
            // MOVL AX, (CX)
            myID = m.idNoInline()
        }
    })
}
```

两件事情浮现在面前:
- `BenchmarkMethodCall_direct`: 感谢内联，调用被简化为一对简单的内存移动指令了。
- `BenchmarkMethodCall_interface`: 因为动态分发的关系，编译器没办法进行调用内联，因此生成的汇编和之前是完全一样了。

几乎不用修改 `BenchmarkMethodCall_interface` 的代码，因为原来的代码也没啥变化。

快速阅览一下"直接"调用的版本:
```Bash
$ go test -run=NONE -o iface_bench_test.bin iface_bench_test.go && \
  perf stat --cpu=1 \
  taskset 2 \
  ./iface_bench_test.bin -test.cpu=1 -test.benchtime=1s -test.count=3 \
      -test.bench='BenchmarkMethodCall_direct/single/inline'
BenchmarkMethodCall_direct/single/inline         	2000000000	         0.35 ns/op
BenchmarkMethodCall_direct/single/inline         	2000000000	         0.34 ns/op
BenchmarkMethodCall_direct/single/inline         	2000000000	         0.34 ns/op

 Performance counter stats for 'CPU(s) 1':

       2464.353001      cpu-clock (msec)          #    1.000 CPUs utilized          
               629      context-switches          #    0.255 K/sec                  
                 1      cpu-migrations            #    0.000 K/sec                  
             7,322      page-faults               #    0.003 M/sec                  
     9,026,867,915      cycles                    #    3.663 GHz                    
    41,580,825,875      instructions              #    4.61  insn per cycle         
     7,027,066,264      branches                  # 2851.485 M/sec                  
         1,134,955      branch-misses             #    0.02% of all branches        

       2.464386341 seconds time elapsed
```

如我所料，运行地飞快，调用的消耗几乎没有了。
被内联的"直接"调用的版本每次需要 ~0.34ns，“interface” 的版本慢了 ~475%，相比前面的 8% 是断崖般的性能差别。

注意，方法固有的分支消失了，从而使 CPU 能够闲下来更有效地并行执行剩下的指令，达到了 4.61 的 IPC ratio。

**Benchmark suite B: 多实例，很多非内联调用，small/big/peseudo-random 迭代**

这第二个 benchmark 系列，主要研究真实世界的场景，比如对象 slice 的迭代的同时进行方法调用。
为了更好的模仿真实场景，我们把内联关闭掉，真实世界的函数都足够复杂，大概率不会被编译器内联(也是存在像标准库中 `sort.Interface` interface 这样的反例的)。

先定义 3 个类似的 benchmark，只在访问对象 slice 上稍有差别；目的是为了模拟对 cache 亲和性的逐级降低:
1. 第一个 case，迭代器按顺序访问数组，调用方法，然后访问完一个元素之后加一。
1. 第二个 case，迭代器依然按顺序访问数组，不过这次其增加的数值比单行的 cache-line 大一些。
1. 最后一个 case，迭代器按伪随机的步骤来访问 slice。

所有的三种 case，我们都需要确保数组足够大，不要能直接装进处理器的 cache 中，以模拟(不太精确)一个忙碌的服务器，正在 CPU cache 和主存都承载高压力的情况下运作。

下面是对处理器属性的复述，我们会根据这些信息设计我们的 benchmark:
```Bash
$ lscpu | sed -nr '/Model name/ s/.*:\s*(.* @ .*)/\1/p'
Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz
$ lscpu | grep cache
L1d cache:           32K
L1i cache:           32K
L2 cache:            256K
L3 cache:            6144K
$ getconf LEVEL1_DCACHE_LINESIZE
64
$ getconf LEVEL1_ICACHE_LINESIZE
64
$ find /sys/devices/system/cpu/cpu0/cache/index{1,2,3} -name "shared_cpu_list" -exec cat {} \;
# (annotations are mine)
0,4 # L1 (hyperthreading)
0,4 # L2 (hyperthreading)
0-7 # L3 (shared + hyperthreading)
```

这是 "直接" 调用的 benchmark 系列(benchmark 标记为 `baseline` 计算的是独立获取 receiver 的成本)，这样我们能从最终的结果把消耗扣出来:
```Go
const _maxSize = 2097152             // 2^21
const _maxSizeModMask = _maxSize - 1 // avoids a mod (%) in the hot path

var _randIndexes = [_maxSize]int{}
func init() {
    rand.Seed(42)
    for i := range _randIndexes {
        _randIndexes[i] = rand.Intn(_maxSize)
    }
}

func BenchmarkMethodCall_direct(b *testing.B) {
    adders := make([]*id32, _maxSize)
    for i := range adders {
        adders[i] = &id32{id: int32(i)}
    }
    runtime.GC()

    var myID int32

    b.Run("many/noinline/small_incr", func(b *testing.B) {
        var m *id32
        b.Run("baseline", func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                m = adders[i&_maxSizeModMask]
            }
        })
        b.Run("call", func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                m = adders[i&_maxSizeModMask]
                myID = m.idNoInline()
            }
        })
    })
    b.Run("many/noinline/big_incr", func(b *testing.B) {
        var m *id32
        b.Run("baseline", func(b *testing.B) {
            j := 0
            for i := 0; i < b.N; i++ {
                m = adders[j&_maxSizeModMask]
                j += 32
            }
        })
        b.Run("call", func(b *testing.B) {
            j := 0
            for i := 0; i < b.N; i++ {
                m = adders[j&_maxSizeModMask]
                myID = m.idNoInline()
                j += 32
            }
        })
    })
    b.Run("many/noinline/random_incr", func(b *testing.B) {
        var m *id32
        b.Run("baseline", func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                m = adders[_randIndexes[i&_maxSizeModMask]]
            }
        })
        b.Run("call", func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                m = adders[_randIndexes[i&_maxSizeModMask]]
                myID = m.idNoInline()
            }
        })
    })
}
```
"interface" 版本的 benchmark 系列完全一样，除了数组是由 interface 而不是指向具体类型的指针:
```Go
func BenchmarkMethodCall_interface(b *testing.B) {
    adders := make([]identifier, _maxSize)
    for i := range adders {
        adders[i] = identifier(&id32{id: int32(i)})
    }
    runtime.GC()

    /* ... */
}
```

“直接”调用的系列，我们得到如下结果:
```Bash
$ go test -run=NONE -o iface_bench_test.bin iface_bench_test.go && \
  benchstat <(
    taskset 2 ./iface_bench_test.bin -test.cpu=1 -test.benchtime=1s -test.count=3 \
      -test.bench='BenchmarkMethodCall_direct/many/noinline')
name                                                  time/op
MethodCall_direct/many/noinline/small_incr/baseline   0.99ns ± 3%
MethodCall_direct/many/noinline/small_incr/call       2.32ns ± 1% # 2.32 - 0.99 = 1.33ns
MethodCall_direct/many/noinline/big_incr/baseline     5.86ns ± 0%
MethodCall_direct/many/noinline/big_incr/call         17.1ns ± 1% # 17.1 - 5.86 = 11.24ns
MethodCall_direct/many/noinline/random_incr/baseline  8.80ns ± 0%
MethodCall_direct/many/noinline/random_incr/call      30.8ns ± 0% # 30.8 - 8.8 = 22ns
```
没啥出乎意料的地方:
1. `small_incr`: 因为 *极其* cache 友好，我们得到了和之前在单实例上循环类似的结果。
2. `big_incr`: 因为强制 CPU 每次迭代的时候取新的 cache-line，我们看到了严重的延迟增加，虽然这个和执行调用本身没啥关系: ~6ns 可以归因于基线，剩下的则是对 receiver 进行解引用以得到它的 `id` 字段以及拷贝返回值带来的影响。
3. `random_incr`: 和 `big_incr` 一样 `big_incr`, 除了在延迟上的增加是因为 A) 伪随机访问和 B) 获取预计算数组的下一个索引(这样会导致 cache miss)。

如上述逻辑所述，不管用哪种方法驱逐 CPU d-cache 并不会显著地影响方法的直接调用(无论是否内联)，只会让其周边的东西都稍微慢一点。

动态分发的话呢？
```Bash
$ go test -run=NONE -o iface_bench_test.bin iface_bench_test.go && \
  benchstat <(
    taskset 2 ./iface_bench_test.bin -test.cpu=1 -test.benchtime=1s -test.count=3 \
      -test.bench='BenchmarkMethodCall_interface/many/inline')
name                                                     time/op
MethodCall_interface/many/noinline/small_incr/baseline   1.38ns ± 0%
MethodCall_interface/many/noinline/small_incr/call       3.48ns ± 0% # 3.48 - 1.38 = 2.1ns
MethodCall_interface/many/noinline/big_incr/baseline     6.86ns ± 0%
MethodCall_interface/many/noinline/big_incr/call         19.6ns ± 1% # 19.6 - 6.86 = 12.74ns
MethodCall_interface/many/noinline/random_incr/baseline  11.0ns ± 0%
MethodCall_interface/many/noinline/random_incr/call      34.7ns ± 0% # 34.7 - 11.0 = 23.7ns
```
结果看起来都极其相似，除了整体上稍微慢了一些，因为我们每次迭代都拷贝了两个 quad-word(i.e. 两个字段都是 `identifier` interface 类型)到 slice 外部而不是一个(指向 `id32` 的指针)。

这个和 "直接"调用的性能差不多多，因为 slice 中的所有 interface 们都共享同一个 `itab`(i.e. 他们都是 `iface<Mather, Adder>` interface)，他们附属的虚表从来没有从 L1d cache 中离开，所以每次迭代获取正确的方法指针都是没啥成本的了。

同样的，组成 `main.(*id32).idNoInline` 方法体的指令也从来没有离开过 L1i cache。

你可能会这么想，实践中我一个 interface 的 slice 可能会包含有很多不同的底层类型(就是 vtable)，这会导致对 L1i 和 L1d cache 的驱逐效果，因为不同的 vtable 会把其它的挤出 cache。
然而这个想法只是理论上成立，有这样的想法，可能是因为你之前写过其它 OOP 语言，比如 C++，并从中积累到的经验。在这种语言中是鼓励使用深层嵌套的类继承，这也是它们用来抽象的主要工具。

如果继承树本身非常大的话，其相关的 vtable 的数量也会大到一定程度，并能在迭代这种持有大量实现的虚类时，把 CPU cache 挤出去了(可以想想 GUI 框架中，所有类型都是 `Widget`，并存储在一个像图一样的数据结构中)；在 C++ 中尤其如此，虚类倾向于定义非常复杂的行为，有时候可能有几十个方法，然后就形成了很大的 vtable 以及到 L1d cache 的较大压力。

Go，在一方面有着完全不同的范式: OOP 被完全扔掉了，类型系统被打平，interface 经常被定义得很小，具有具体的行为(平均下来只有较少的方法，只要实现就可以隐式满足接口)。而不是一种基于复杂的多层继承的抽象。
在实践上，我发现 Go 语言很少需要在有各种不同的底层类型的 interface 上进行迭代。当然，你可能比我知道更多的 case。

下面是打开了内联的 “直接” 调用的版本的结果，给那些好奇心旺盛的人:
```Bash
name                                                time/op
MethodCall_direct/many/inline/small_incr            0.97ns ± 1% # 0.97ns
MethodCall_direct/many/inline/big_incr/baseline     5.96ns ± 1%
MethodCall_direct/many/inline/big_incr/call         11.9ns ± 1% # 11.9 - 5.96 = 5.94ns
MethodCall_direct/many/inline/random_incr/baseline  9.20ns ± 1%
MethodCall_direct/many/inline/random_incr/call      16.9ns ± 1% # 16.9 - 9.2 = 7.7ns
```
编译器如果可以内联该调用的话，会使"直接"调用版本大概比 "interface" 版本快 2 到 3 倍。
之前也提过，因为现在的编译器的局限性，实践中大多数的函数都不会被内联，所以这种性能提升是非常少见的。当然，更为常见的是你没有别的选择，只能采用这种基于虚表的调用。

**Conclusion**

想要尽量有效地衡量虚表的函数调用看起来是一件比较复杂的尝试，因为其效果是众多交错复杂的边际效应，再加上现代硬件的复杂实现组成的共同结果。

*在 Go 语言中*, 感谢语言的设计范式，考虑到当前编译器限制和内联的话，可以认为动态分发基本上是没什么成本的。 不过如果还是怀疑这一点的话，可以随时对代码的 hot 路径进行评估，并对性能评测进行计算，来确定到底动态分发是否会为自己的系统带来问题。

(NOTE: 本书的晚些章节会研究编译器的内联能力)

## Special cases & compiler tricks

本节会 review 一些和 interface 打交道时，每天都会碰到的特殊 case。

现在你对 interface 的工作原理应该已经有了清晰的认识，所以下面我们的讲解会简略一些。

### The empty interface

空接口的的数据结构和直觉推测差不多:  一个不带 `itab` 的 `eface` 结构。
这样做有两个原因:
1. 空接口中没有任何方法，和动态分发相关的东西都可以从数据结构中移除。
2. 干掉虚表之后，接口本身的类型，这里注意不要和接口中数据的类型混了，始终都是相同的(i.e. 我们说的是 *这个* 空接口而不是 *一个* 空接口)

*NOTE: 和前面我们给 `iface` 用的记号差不多，我们把持有 T 类型数据的空接口标记为 `eface<T>`*

`eface` 是表示 runtime 中空接口的根类型 ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L148-L151)).  
定义差不多是这样:
```Go
type eface struct { // 16 bytes on a 64bit arch
    _type *_type
    data  unsafe.Pointer
}
```
其中 `_type` 持有 `data` 指针指向的数据类型的信息。
`itab` 被完全干掉了。

尽管空接口理论上可以重用 `iface` 数据结构(因为 `iface` 可以算是 `eface` 的一个超集)，runtime 还是选择对这两种 interface 进行区分，主要有两个理由: 为了节省空间，以及代码清晰。

### Interface holding a scalar type

本章早些部分的 ([#Anatomy of an Interface](#overview-of-the-datastructures))，我们提到了即使将一个标量类型存到 interface 里也会导致堆内存分配。
现在是时机来研究原因和过程了。

看看这两个 benchmark ([eface_scalar_test.go](./eface_scalar_test.go)):
```Go
func BenchmarkEfaceScalar(b *testing.B) {
    var Uint uint32
    b.Run("uint32", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            Uint = uint32(i)
        }
    })
    var Eface interface{}
    b.Run("eface32", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            Eface = uint32(i)
        }
    })
}
```
```Bash
$ go test -benchmem -bench=. ./eface_scalar_test.go
BenchmarkEfaceScalar/uint32-8         	2000000000	   0.54 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface32-8        	 100000000	   12.3 ns/op	  4 B/op     1 allocs/op
```
1. 对于简单的赋值操作来说，这已经是两个数量级的差距了，并且
2. 这里可以看到第二个 benchmark 需要在每次迭代的时候分配 4 个额外字节。

显然，第二个 case 中代码背后隐藏了沉重的机关，这些机关被关掉了: 我们需要研究一下生成的汇编内容。

第一个 benchmark，编译器如愿生成赋值操作:
```Assembly
;; Uint = uint32(i)
0x000d MOVL	DX, (AX)
```

第二个 benchmark 要复杂很多:
```Assembly
;; Eface = uint32(i)
0x0050 MOVL	CX, ""..autotmp_3+36(SP)
0x0054 LEAQ	type.uint32(SB), AX
0x005b MOVQ	AX, (SP)
0x005f LEAQ	""..autotmp_3+36(SP), DX
0x0064 MOVQ	DX, 8(SP)
0x0069 CALL	runtime.convT2E32(SB)
0x006e MOVQ	24(SP), AX
0x0073 MOVQ	16(SP), CX
0x0078 MOVQ	"".&Eface+48(SP), DX
0x007d MOVQ	CX, (DX)
0x0080 MOVL	runtime.writeBarrier(SB), CX
0x0086 LEAQ	8(DX), DI
0x008a TESTL	CX, CX
0x008c JNE	148
0x008e MOVQ	AX, 8(DX)
0x0092 JMP	46
0x0094 CALL	runtime.gcWriteBarrier(SB)
0x0099 JMP	46
```
这还 *只* 是赋值，不是完整的 benchmark！
我们一句一句研究一下这段代码。

**Step 1: Create the interface**

```Assembly
0x0050 MOVL	CX, ""..autotmp_3+36(SP)
0x0054 LEAQ	type.uint32(SB), AX
0x005b MOVQ	AX, (SP)
0x005f LEAQ	""..autotmp_3+36(SP), DX
0x0064 MOVQ	DX, 8(SP)
0x0069 CALL	runtime.convT2E32(SB)
0x006e MOVQ	24(SP), AX
0x0073 MOVQ	16(SP), CX
```

第一段代码实例化了之后要赋值给 `Eface` 的空接口 `eface<uint32>`。

在创建 interface 的章节 ([#Creating an interface](#creating-an-interface)) 我们已经研究过类似的代码了，这里的代码除了调用的是  `runtime.convT2I32` 而不是 `runtime.convT2E32`，没啥区别。

看来 `runtime.convT2I32` 和 `runtime.convT2E32` 都是同一个大家族的函数成员，这个家族的函数的工作就是用标量实例化一个特定的 interface 或者空 interface(特殊情况下也可能是 string 或者 slice)。
该函数族由 10 个符号组成，是由`(eface/iface, 16/32/64/string/slice)` 两两组合而成:
```Go
// empty interface from scalar value
func convT2E16(t *_type, elem unsafe.Pointer) (e eface) {}
func convT2E32(t *_type, elem unsafe.Pointer) (e eface) {}
func convT2E64(t *_type, elem unsafe.Pointer) (e eface) {}
func convT2Estring(t *_type, elem unsafe.Pointer) (e eface) {}
func convT2Eslice(t *_type, elem unsafe.Pointer) (e eface) {}

// interface from scalar value
func convT2I16(tab *itab, elem unsafe.Pointer) (i iface) {}
func convT2I32(tab *itab, elem unsafe.Pointer) (i iface) {}
func convT2I64(tab *itab, elem unsafe.Pointer) (i iface) {}
func convT2Istring(tab *itab, elem unsafe.Pointer) (i iface) {}
func convT2Islice(tab *itab, elem unsafe.Pointer) (i iface) {}
```
(*可以看到，没有 `convT2E8` 和 `convT2I8` 方法；这是因为我们在本节末尾所描述的编译器优化*)

所有函数干的事情都一样，他们唯一的区别就是返回的值的类型(`iface` vs. `eface`)以及他们在堆上分配的内存大小。

再来更仔细地研究一下比如 `runtime.convT2E32` ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L308-L325)):
```Go
func convT2E32(t *_type, elem unsafe.Pointer) (e eface) {
    /* ...omitted debug stuff... */
    var x unsafe.Pointer
    if *(*uint32)(elem) == 0 {
        x = unsafe.Pointer(&zeroVal[0])
    } else {
        x = mallocgc(4, t, false)
        *(*uint32)(x) = *(*uint32)(elem)
    }
    e._type = t
    e.data = x
    return
}
```

这个函数初始化了 `eface` 结构体的 `_type` 字段，该结构由调用方(记住: 返回值都是由 caller 在它自己的栈帧上分配的)作为第一个参数传入。
`eface` 的 `data` 字段依赖于第二个参数 `elem`:
- 如果 `elem` 是零值, `e.data` 就被初始化，指向 `runtime.zeroVal`，这是 runtime 提供的一个用来代表零值的特殊全局变量。我们在下一节会更多地讨论这个特殊变量。
- 如果 `elem` 非零，函数会在堆上分配 4 个字节(`x = mallocgc(4, t, false)`)，初始化 `elem`(`*(*uint32)(x) = *(*uint32)(elem)`)这个指针指向的这 4 个字节的内容，然后把指针指向 `e.data`。

这种情况下，`e._type` 持有 `type.uint32` 的地址(`LEAQ type.uint32(SB), AX`)，这是由标准库实现的，其地址只有链接期才能由 stdlib 知晓:
```Bash
$ go tool nm eface_scalar_test.o | grep 'type\.uint32'
         U type.uint32
```
(`U` 表示该符号不在目标文件中定义，并将(很可能)由另一个目标，在链接期提供(i.e. 这个 case 的情况下是标准库))

**Step 2: Assign the result (part 1)**

```Assembly
0x0078 MOVQ	"".&Eface+48(SP), DX
0x007d MOVQ	CX, (DX)		;; Eface._type = ret._type
```
`runtime.convT2E32` 的结果被赋值给我们的 `Eface` 变量了..真的么？

实际上，现在来看，返回值只有 `_type` 字段被赋值给了 `Eface._type`，`data` 字段没办法被拷贝。

**Step 3: Assign the result (part 2) or ask the garbage collector to**

```Assembly
0x0080 MOVL	runtime.writeBarrier(SB), CX
0x0086 LEAQ	8(DX), DI	;; Eface.data = ret.data (indirectly via runtime.gcWriteBarrier)
0x008a TESTL	CX, CX
0x008c JNE	148
0x008e MOVQ	AX, 8(DX)	;; Eface.data = ret.data (direct)
0x0092 JMP	46
0x0094 CALL	runtime.gcWriteBarrier(SB)
0x0099 JMP	46
```

这里显而易见的复杂度，是由将返回的 `eface` 的 `data` 指针赋值给 `Eface.data` 造成的影响: 由于我们操作了程序的内存图(i.e. 哪部分内存引用了另外的哪部分内存)，这种情况下需要将这种变更通知垃圾收集器，因为这时候在后台可能有正在运行的垃圾收集任务。

这也被称为 write barrier，是 Go 的 *并发* 垃圾收集的直接结果。
如果听起来有点懵，先不要太担心；本书的下一个章节将提供 Go 垃圾收集的全面审视。
现在的话，只要记住在汇编中看到对 `runtime.gcWriteBarrier` 的调用代码的话，一定是和指针操作相关，并且必须要通知垃圾收集器就行了。

总结一下，最后一段代码做了下面两件事情:
- 如果 write-barrier 当前不活跃，把 `ret.data` 赋值给 `Eface.data` (`MOVQ AX, 8(DX)`)。
- 如果 write-barrier 活跃的话，礼貌地要求垃圾收集器替我们把工作给做了 (`LEAQ 8(DX), DI` + `CALL runtime.gcWriteBarrier(SB)`).

(*同样的，先不要太在意这里的函数调用*)

瞧，我们已经得到了一个保存了 (`uint32`) 标量类型的完整的 interface。

**Conclusion**

把标量值绑定到 interface 上实践中并不会经常发生，从很多方面讲都会导致较大的成本，因此了解背后的原理就比较重要了。

提到成本的话，我们已经提过编译器实现了各种各样的 trick 来避免特定情况下的内存分配；我们以 3 种编译器层面做的优化的例子来结束本节的内容。

**Interface trick 1: Byte-sized values**

先来看这段实例化 `eface<uint8>` 的代码的 benchmark ([eface_scalar_test.go](./eface_scalar_test.go)):
```Go
func BenchmarkEfaceScalar(b *testing.B) {
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
}
```
```Bash
$ go test -benchmem -bench=BenchmarkEfaceScalar/eface8 ./eface_scalar_test.go
BenchmarkEfaceScalar/eface8-8         	2000000000	   0.88 ns/op	  0 B/op     0 allocs/op
```

可以注意到，在值为一个字节大小的情况下，编译器会避免调用 `runtime.convT2E`/`runtime.convT2I` 和相关的堆内存分配。取而代之，直接重用了我们需要的， runtime 暴露给我们的已经初始化好的， 1 个字节大小的值: `LEAQ    runtime.staticbytes(SB), R8`.

`runtime.staticbytes` 可以在 [src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L619-L653) 找到，长下面这样:
```Go
// staticbytes is used to avoid convT2E for byte-sized values.
var staticbytes = [...]byte{
    0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
    0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
    0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f,
    0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f,
    0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f,
    0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x5f,
    0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f,
    0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f,
    0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f,
    0x90, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9e, 0x9f,
    0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf,
    0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf,
    0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf,
    0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf,
    0xe0, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xeb, 0xec, 0xed, 0xee, 0xef,
    0xf0, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xfb, 0xfc, 0xfd, 0xfe, 0xff,
}
```
只要使用这个数组中正确的偏移量，编译器就可以有效地避免额外的堆内存分配，并可以引用用一个字节的任意值了。

不过这里有些不对头，你能看出来么？
生成的代码会嵌入和 write-barrier 相关的所有工具，尽管我们操作的指针只是持有了一些全局变量的地址，而这些全局变量的生命周期和整个程序是一样的。
也就是说，`runtime.staticbytes` 永远都不会被垃圾收集，无论它被哪一部分持有引用，或者没有被引用，所以我们不需要在意这种情况下的 write-barrier 成本。

**Interface trick 2: Static inference**

这是用编译时才知道的值来实例化 `eface<uint64>` 所做的 benchmark ([eface_scalar_test.go](./eface_scalar_test.go)):
```Go
func BenchmarkEfaceScalar(b *testing.B) {
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
```
```Bash
$ go test -benchmem -bench=BenchmarkEfaceScalar/eface-static ./eface_scalar_test.go
BenchmarkEfaceScalar/eface-static-8    	2000000000	   0.81 ns/op	  0 B/op     0 allocs/op
```

从生成的汇编可以看出来编译器将 `runtime.convT2E64` 的调用完全优化没了，并且通过加载自动生成的全局变量地址直接构建好了空 interface，这个全局地址已经包含了我们想要的值: `LEAQ "".statictmp_0(SB), SI` (注意这里的 `(SB)` 部分，代表的是一个全局变量)。

用之前我们搞的脚本 `dump_sym.sh`，能够更好地把正在发生的事情可视化出来:
```Bash
$ GOOS=linux GOARCH=amd64 go tool compile eface_scalar_test.go
$ GOOS=linux GOARCH=amd64 go tool link -o eface_scalar_test.bin eface_scalar_test.o
$ ./dump_sym.sh eface_scalar_test.bin .rodata main.statictmp_0
.rodata file-offset: 655360
.rodata VMA: 4849664
main.statictmp_0 VMA: 5145768
main.statictmp_0 SIZE: 8

0000000 002a 0000 0000 0000                    
0000008
```
像期望的一样, `main.statictmp_0` 是一个 8-字节变量，其值为 `0x000000000000002a` 就是 `$42`.

**Interface trick 3: Zero-values**

最后一个技巧，看看下面这个用零值实例化 `eface<uint32>` 的 benchmark ([eface_scalar_test.go](./eface_scalar_test.go)):
```Go
func BenchmarkEfaceScalar(b *testing.B) {
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
            Eface = uint32(i - i) // outsmart the compiler (avoid static inference)
        }
    })
}
```
```Bash
$ go test -benchmem -bench=BenchmarkEfaceScalar/eface-zero ./eface_scalar_test.go
BenchmarkEfaceScalar/eface-zeroval-8  	 500000000	   3.14 ns/op	  0 B/op     0 allocs/op
```

首先，注意我们如何利用 `uint32(i - i)`，而不是 `uint32(0)` 来避免编译器进行优化 #2(static inference)。
(*是的，我们可以只声明一个全局的零值变量，这样编译器就会被强制采用更保守的路线。。不过再次的，我们是为了能有一点乐子。不要那么较真*)

生成的代码看起来比较正常，分配的 case。。还是没有发生分配，什么情况？

早些时候剖析`runtime.convT2E32` 的时候有提到，这里的内存分配能够用类似 #1(单字节值) 的技巧完全优化掉: 当一些代码需要引用零值变量，编译器会简单地提供给它一个 runtime 暴露出的全局变量，该变量已经被初始化为零了。

和 `runtime.staticbytes` 类似，我们可以在 runtime 的代码里找到这个变量 ([src/runtime/hashmap.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/hashmap.go#L1248-L1249)):
```Go
const maxZero = 1024 // must match value in ../cmd/compile/internal/gc/walk.go
var zeroVal [maxZero]byte
```

这就结束了我们的优化之旅。
我们汇总一下之前的所有 benchmark 来结束这一节:
```Bash
$ go test -benchmem -bench=. ./eface_scalar_test.go
BenchmarkEfaceScalar/uint32-8         	2000000000	   0.54 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface32-8        	 100000000	   12.3 ns/op	  4 B/op     1 allocs/op
BenchmarkEfaceScalar/eface8-8         	2000000000	   0.88 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface-zeroval-8  	 500000000	   3.14 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface-static-8    	2000000000	   0.81 ns/op	  0 B/op     0 allocs/op
```

### A word about zero-values

像我们已经看到的，`runtime.convT2*` 族函数在 interface 持有的数据恰好引用了零值时，会避免堆上的内存分配。
这种优化并不为 interface 所特有，在 Go 的 runtime 中被广泛应用，只要有指针指向了零值，就会避免不必要的内存分配，只要将该指针指向一个 runtime 暴露出的始终为零的特殊变量的地址即可。

可以用一个简单的程序确认 ([zeroval.go](./zeroval.go)):
```Go
//go:linkname zeroVal runtime.zeroVal
var zeroVal uintptr

type eface struct{ _type, data unsafe.Pointer }

func main() {
    x := 42
    var i interface{} = x - x // outsmart the compiler (avoid static inference)

    fmt.Printf("zeroVal = %p\n", &zeroVal)
    fmt.Printf("      i = %p\n", ((*eface)(unsafe.Pointer(&i))).data)
}
```
```Bash
$ go run zeroval.go
zeroVal = 0x5458e0
      i = 0x5458e0
```
不出所料。

注意 `//go:linkname` 指令使我们可以引用外部的符号:
> The //go:linkname directive instructs the compiler to use “importpath.name” as the object file symbol name for the variable or function declared as “localname” in the source code. Because this directive can subvert the type system and package modularity, it is only enabled in files that have imported "unsafe".

### A tangent about zero-size variables

和零值类似的套路，Go 程序的常见的一个技巧是使用大小为 0 的对象(例如 `struct{}{}`) 不会进行任何内存分配。
Go 的官方 spec (本章最后有链接) 这么几句话对此进行了解释:
> A struct or array type has size zero if it contains no fields (or elements, respectively) that have a size greater than zero.
> Two distinct zero-size variables may have the same address in memory.

"may have the same address in memory" 这句话中的 "may" 表明编译器不保证这件事一定发生，尽管实际上总会发生，并且是现在官方 Go 编译器(`gc`)就是这样的。

用简单的程序来确认 ([zerobase.go](./zerobase.go)):
```Go
func main() {
    var s struct{}
    var a [42]struct{}

    fmt.Printf("s = % p\n", &s)
    fmt.Printf("a = % p\n", &a)
}
```
```Bash
$ go run zerobase.go
s = 0x546fa8
a = 0x546fa8
```

假如想知道地址后面隐藏了什么东西，只要简单地来看一下二进制文件的内容:
```Bash
$ go build -o zerobase.bin zerobase.go && objdump -t zerobase.bin | grep 546fa8
0000000000546fa8 g     O .noptrbss	0000000000000008 runtime.zerobase
```
下面就只需要在 runtime 源代码中找 `runtime.zerobase` 这个变量就行了 ([src/runtime/malloc.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/malloc.go#L516-L517)):
```Go
// base address for all 0-byte allocations
var zerobase uintptr
```

如果我们想要非常非常精确地确认这件事:
```Go
//go:linkname zerobase runtime.zerobase
var zerobase uintptr

func main() {
    var s struct{}
    var a [42]struct{}

    fmt.Printf("zerobase = %p\n", &zerobase)
    fmt.Printf("       s = %p\n", &s)
    fmt.Printf("       a = %p\n", &a)
}
```
```Bash
$ go run zerobase.go
zerobase = 0x546fa8
       s = 0x546fa8
       a = 0x546fa8
```

## Interface composition

interface 组合实在是没啥特殊的，这只是编译器提供的一种语法糖而已。

看一下下面的程序 ([compound_interface.go](./compound_interface.go)):
```Go
type Adder interface{ Add(a, b int32) int32 }
type Subber interface{ Sub(a, b int32) int32 }
type Mather interface {
    Adder
    Subber
}

type Calculator struct{ id int32 }
func (c *Calculator) Add(a, b int32) int32 { return a + b }
func (c *Calculator) Sub(a, b int32) int32 { return a - b }

func main() {
    calc := Calculator{id: 6754}
    var m Mather = &calc
    m.Sub(10, 32)
}
```

像往常一样，编译器会生成和 `itab` 对应的 `iface<Mather, *Calculator>`:
```Bash
$ GOOS=linux GOARCH=amd64 go tool compile -S compound_interface.go | \
  grep -A 7 '^go.itab.\*"".Calculator,"".Mather'
go.itab.*"".Calculator,"".Mather SRODATA dupok size=40
    0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
    0x0010 5e 33 ca c8 00 00 00 00 00 00 00 00 00 00 00 00  ^3..............
    0x0020 00 00 00 00 00 00 00 00                          ........
    rel 0+8 t=1 type."".Mather+0
    rel 8+8 t=1 type.*"".Calculator+0
    rel 24+8 t=1 "".(*Calculator).Add+0
    rel 32+8 t=1 "".(*Calculator).Sub+0
```
我们能从重定向指令中看到编译器生成的虚表持有了 `Adder` 和 `Subber` 方法，像我们所期望的一样:
```
rel 24+8 t=1 "".(*Calculator).Add+0
rel 32+8 t=1 "".(*Calculator).Sub+0
```

如之前所述，interface 组合没有什么秘密武器。

提一句不相关的，这个小程序演示了我们还没见过的一些东西: 因为生成的 `itab` 是为指向构建器的 *指针* 所定制的，与具体的值相对应，这一点在它的符号名 (`go.itab.*"".Calculator,"".Mather`) 上和内嵌的 `_type` (`type.*"".Calculator`) 上都有所反映。

和我们本章见到的一样，这和命名方法的符号所使用的语义也是一致的。

## Assertions

我们会以类型断言来结束这一章，会同时从实现和成本两方面来讨论。

### Type assertions

先看看这个简单的程序 ([eface_to_type.go](./eface_to_type.go)):
```Go
var j uint32
var Eface interface{} // outsmart compiler (avoid static inference)

func assertion() {
    i := uint64(42)
    Eface = i
    j = Eface.(uint32)
}
```

下面是带注释的汇编版本的 `j = Eface.(uint32)`:
```Assembly
0x0065 00101 MOVQ	"".Eface(SB), AX		;; AX = Eface._type
0x006c 00108 MOVQ	"".Eface+8(SB), CX		;; CX = Eface.data
0x0073 00115 LEAQ	type.uint32(SB), DX		;; DX = type.uint32
0x007a 00122 CMPQ	AX, DX				;; Eface._type == type.uint32 ?
0x007d 00125 JNE	162				;; no? panic our way outta here
0x007f 00127 MOVL	(CX), AX			;; AX = *Eface.data
0x0081 00129 MOVL	AX, "".j(SB)			;; j = AX = *Eface.data
;; exit
0x0087 00135 MOVQ	40(SP), BP
0x008c 00140 ADDQ	$48, SP
0x0090 00144 RET
;; panic: interface conversion: <iface> is <have>, not <want>
0x00a2 00162 MOVQ	AX, (SP)			;; have: Eface._type
0x00a6 00166 MOVQ	DX, 8(SP)			;; want: type.uint32
0x00ab 00171 LEAQ	type.interface {}(SB), AX	;; AX = type.interface{} (eface)
0x00b2 00178 MOVQ	AX, 16(SP)			;; iface: AX
0x00b7 00183 CALL	runtime.panicdottypeE(SB)	;; func panicdottypeE(have, want, iface *_type)
0x00bc 00188 UNDEF
0x00be 00190 NOP
```

也没啥出人意料的: 代码比较了 `Eface._type` 持有的地址和 `type.uint32` 持有的地址，之前也见过，这是标准库暴露出的全局符号，它持有的 `_type` 结构描述了 `uint32` 这个类型。
如果 `_type` 指针匹配，那么我们可以一切正常地将 `*Eface.data` 赋值给 `j`；否则的话，我们需要调用 `runtime.panicdottypeE` 来抛出 panic 信息，并精确地描述这种不匹配。

`runtime.panicdottypeE` 是一个 _非常_ 简单的函数，只做了顾名思义的工作 ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L235-L245)):
```Go
// panicdottypeE is called when doing an e.(T) conversion and the conversion fails.
// have = the dynamic type we have.
// want = the static type we're trying to convert to.
// iface = the static type we're converting from.
func panicdottypeE(have, want, iface *_type) {
    haveString := ""
    if have != nil {
        haveString = have.string()
    }
    panic(&TypeAssertionError{iface.string(), haveString, want.string(), ""})
}
```

**What about performance?**

来看看我们都得到了些啥: 一堆从主存拿到的 `MOV` 指令，一个*很*容易预测的分支，一个指针解引用(`j = *Eface.data`) (这里是因为一开始我们就用具体值实例化了 interface 变量，否则我们就只能直接拷贝 `Eface.data` 指针了)。

这里不应该做 benchmark。
和我们之前评估的，动态分发的成本类似，理论上这也是几乎没啥成本的。如果说实际会给你带来多少消耗的话，要取决于你的代码路径是否设计得对 cache 友好。
再在这里做 benchmark 的话，也得不到真实环境下的结果，可能还会歪曲事实。

总结一下，我们得到了一个常见的守旧的建议: 按你的场景进行测试，检查你的处理器性能瓶颈，并确定这对你的 hot path 来说到底会不会产生可见的影响。
可能会。也可能不会。大多数情况下不会。

### Type-switches

类型 switch 稍微有点 trick。看看下面的代码 ([eface_to_type.go](./eface_to_type.go)):
```Go
var j uint32
var Eface interface{} // outsmart compiler (avoid static inference)

func typeSwitch() {
    i := uint32(42)
    Eface = i
    switch v := Eface.(type) {
    case uint16:
        j = uint32(v)
    case uint32:
        j = v
    }
}
```

这个简单的类型 switch 语句被翻译成了如下汇编(已注释):
```Assembly
;; switch v := Eface.(type)
0x0065 00101 MOVQ	"".Eface(SB), AX	;; AX = Eface._type
0x006c 00108 MOVQ	"".Eface+8(SB), CX	;; CX = Eface.data
0x0073 00115 TESTQ	AX, AX			;; Eface._type == nil ?
0x0076 00118 JEQ	153			;; yes? exit the switch
0x0078 00120 MOVL	16(AX), DX		;; DX = Eface.type._hash
;; case uint32
0x007b 00123 CMPL	DX, $-800397251		;; Eface.type._hash == type.uint32.hash ?
0x0081 00129 JNE	163			;; no? go to next case (uint16)
0x0083 00131 LEAQ	type.uint32(SB), BX	;; BX = type.uint32
0x008a 00138 CMPQ	BX, AX			;; type.uint32 == Eface._type ? (hash collision?)
0x008d 00141 JNE	206			;; no? clear BX and go to next case (uint16)
0x008f 00143 MOVL	(CX), BX		;; BX = *Eface.data
0x0091 00145 JNE	163			;; landsite for indirect jump starting at 0x00d3
0x0093 00147 MOVL	BX, "".j(SB)		;; j = BX = *Eface.data
;; exit
0x0099 00153 MOVQ	40(SP), BP
0x009e 00158 ADDQ	$48, SP
0x00a2 00162 RET
;; case uint16
0x00a3 00163 CMPL	DX, $-269349216		;; Eface.type._hash == type.uint16.hash ?
0x00a9 00169 JNE	153			;; no? exit the switch
0x00ab 00171 LEAQ	type.uint16(SB), DX	;; DX = type.uint16
0x00b2 00178 CMPQ	DX, AX			;; type.uint16 == Eface._type ? (hash collision?)
0x00b5 00181 JNE	199			;; no? clear AX and exit the switch
0x00b7 00183 MOVWLZX	(CX), AX		;; AX = uint16(*Eface.data)
0x00ba 00186 JNE	153			;; landsite for indirect jump starting at 0x00cc
0x00bc 00188 MOVWLZX	AX, AX			;; AX = uint16(AX) (redundant)
0x00bf 00191 MOVL	AX, "".j(SB)		;; j = AX = *Eface.data
0x00c5 00197 JMP	153			;; we're done, exit the switch
;; indirect jump table
0x00c7 00199 MOVL	$0, AX			;; AX = $0
0x00cc 00204 JMP	186			;; indirect jump to 153 (exit)
0x00ce 00206 MOVL	$0, BX			;; BX = $0
0x00d3 00211 JMP	145			;; indirect jump to 163 (case uint16)
```

再一次，如果你精心地单步调试生存的代码并仔细阅读对应的注释的话，你会发现这里也没啥黑魔法。
控制流虽然一开始看上去错综复杂，跳过来跳回去，不过这些代码确实是 Go 原始代码翻译出的最精确的结果。

有一些有意思的事情需要注意:

**Note 1: Layout**

首先，注意生成代码的整体布局，这和原始的 switch 语句是比较接近的:
1. 我们能找到一块包含初始化指令的块，加载变量的 `_type`，然后为了以防万一检查 `nil` 指针。
2. 然后是 N 个逻辑块，每一块对应代码中 switch 语句的其中一个 case。
3. 最后一块定义了一种间接表跳转，使控制流能从一个 case 跳到下一个 case时，把已被污染的寄存器恢复原状。

有了事后的明确认识，第二点非常重要，它说明类型-switch 生成的指令数目只和它所描述的 case 的相关。
实践上，这会导致令人惊讶的性能问题，例如，一个有很多 case 的大规模的类型-switch 语句，在猜错分支时，会导致 L1i cache 直接被冲掉。

另一个有意思的是，我们的 switch 语句的布局，和生成的代码的 case 的顺序没什么关系。在我们 Go 的原始代码中， `case uint16` 先到，然后是 `case uint32`。在编译器生成的汇编代码中，这两个 case 的顺序被调换了，`case uint32` 在前面，而 `case uint16` 在后面。
这个特定的 case 下，这种重排操作对我们来说是净赚，不过也只是运气好罢了。实际上如果你花一点时间对类型-switch 做实验的话，尤其是那些有两个 case 以上的 switch，你会发现编译器会以某种固定的启发法对 case 进行洗牌。
这种乱序的方法是啥，我不清楚(如果你清楚，欢迎告诉我)。

**Note 2: O(n)**

第二，注意控制流是完全闭着眼睛从一个 case 跳到下一个的，直到其落在了结果为 true 的 case 并最终到达 switch 语句的边界。

因为实在太浅显，可能都让人想放弃思考了("肯定还是之前那样啊，还能有啥特殊的")，不过在高层次思考的时候还是比较容易忽视一些问题。实践中，类型断言意味着其成本会随 case 的数量增加线性增长: 其成本为 `O(n)`。
同样的，对 N 个 case 的 type-switch 语句进行求值和 N 个类型断言语句有一样的时间复杂度。这里也没有什么魔法。

只需要几个 benchmark 就可以确认 ([eface_to_type_test.go](./eface_to_type_test.go)):
```Go
var j uint32
var eface interface{} = uint32(42)

func BenchmarkEfaceToType(b *testing.B) {
    b.Run("switch-small", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            switch v := eface.(type) {
            case int8:
                j = uint32(v)
            case int16:
                j = uint32(v)
            default:
                j = v.(uint32)
            }
        }
    })
    b.Run("switch-big", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            switch v := eface.(type) {
            case int8:
                j = uint32(v)
            case int16:
                j = uint32(v)
            case int32:
                j = uint32(v)
            case int64:
                j = uint32(v)
            case uint8:
                j = uint32(v)
            case uint16:
                j = uint32(v)
            case uint64:
                j = uint32(v)
            default:
                j = v.(uint32)
            }
        }
    })
}
```
```Bash
benchstat <(go test -benchtime=1s -bench=. -count=3 ./eface_to_type_test.go)
name                        time/op
EfaceToType/switch-small-8  1.91ns ± 2%
EfaceToType/switch-big-8    3.52ns ± 1%
```
因为考虑更多的 case，第二个类型-switch 几乎花费了两倍的迭代时间。

这里给读者留一个练习题，试着给上面任意一个 benchmark 增加一个 `case uint32`，会看到他们的性能戏剧性地提升了:
```Bash
benchstat <(go test -benchtime=1s -bench=. -count=3 ./eface_to_type_test.go)
name                        time/op
EfaceToType/switch-small-8  1.63ns ± 1%
EfaceToType/switch-big-8    2.17ns ± 1%
```
使用本章学到的所有工具和知识，你应该能够解释这些数字背后的原理。玩的开心！

**Note 3: Type hashes & pointer comparisons**

最后，注意每种 case 下的类型比较都是由两个阶段组成的:
1. 比较类型 hash(`_type.hash`)，然后
2. 如果 match 的话，直接比较两个 `_type` 指针的内存地址。

由于每一个 `_type` 结构都是由编译器一次性生成，并存储在 `.rodata` 段的全局变量中的，编译器保证每一个类型在程序的生命周期内都有唯一的地址。

在这样的上下文下，这个额外的指针比较就可以帮助我们来确定两者确实一样，而不是发生了哈希碰撞。。不过这也带来了一个显而易见的问题: 为什么不直接进行后面这步比较，而干掉哈希比较呢？尤其是简单的类型断言，像我们前面所见，压根儿都不会用类型哈希。
关于答案，我没有确凿的证据，希望能够得到一些提示。像往常一样，如果你知道，就开 issue 告诉我。

关于类型哈希，我们怎么知道 `$-800397251` 对应 `type.uint32.hash` 而 `$-269349216` 对应 `type.uint16.hash`，你可能比较想知道。比较麻烦的方法，当然 ([eface_type_hash.go](./eface_type_hash.go)):
```Go
// simplified definitions of runtime's eface & _type types
type eface struct {
    _type *_type
    data  unsafe.Pointer
}
type _type struct {
    size    uintptr
    ptrdata uintptr
    hash    uint32
    /* omitted lotta fields */
}

var Eface interface{}
func main() {
    Eface = uint32(42)
    fmt.Printf("eface<uint32>._type.hash = %d\n",
        int32((*eface)(unsafe.Pointer(&Eface))._type.hash))

    Eface = uint16(42)
    fmt.Printf("eface<uint16>._type.hash = %d\n",
        int32((*eface)(unsafe.Pointer(&Eface))._type.hash))
}
```
```
$ go run eface_type_hash.go
eface<uint32>._type.hash = -800397251
eface<uint16>._type.hash = -269349216
```

## Conclusion

这就是 interface 相关的所有内容了。

我希望这一章能帮你找到你对 interface 和内部原理的所有问题的答案。更重要的是，希望本章给你提供了你想要深入研究任何知识时所需要的所有必要工具和技能。

如果你有问题或者建议，不要犹豫，看个 issue，带上 `chapter:2` 的前缀！

## Links

- [[Official] Go 1.1 Function Calls](https://docs.google.com/document/d/1bMwCey-gmqZVTpRax-ESeVuZGmjwbocYs1iHplK-cjo/pub)
- [[Official] The Go Programming Language Specification](https://golang.org/ref/spec)
- [The Gold linker by Ian Lance Taylor](https://lwn.net/Articles/276782/)
- [ELF: a linux executable walkthrough](https://i.imgur.com/EL7lT1i.png)
- [VMA vs LMA?](https://www.embeddedrelated.com/showthread/comp.arch.embedded/77071-1.php)
- [In C++ why and how are virtual functions slower?](https://softwareengineering.stackexchange.com/questions/191637/in-c-why-and-how-are-virtual-functions-slower)
- [The cost of dynamic (virtual calls) vs. static (CRTP) dispatch in C++](https://eli.thegreenplace.net/2013/12/05/the-cost-of-dynamic-virtual-calls-vs-static-crtp-dispatch-in-c)
- [Why is it faster to process a sorted array than an unsorted array?](https://stackoverflow.com/a/11227902)
- [Is accessing data in the heap faster than from the stack?](https://stackoverflow.com/a/24057744)
- [CPU cache](https://en.wikipedia.org/wiki/CPU_cache)
- [CppCon 2014: Mike Acton "Data-Oriented Design and C++"](https://www.youtube.com/watch?v=rX0ItVEVjHc)
- [CppCon 2017: Chandler Carruth "Going Nowhere Faster"](https://www.youtube.com/watch?v=2EWejmkKlxs)
- [What is the difference between MOV and LEA?](https://stackoverflow.com/a/1699778)
- [Issue #24631 (golang/go): *testing: don't truncate allocs/op*](https://github.com/golang/go/issues/24631)