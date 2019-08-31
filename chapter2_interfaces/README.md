<!-- Copyright © 2018 Clement Rey <cr.rey.clement@gmail.com>. -->
<!-- Licensed under the BY-NC-SA Creative Commons 4.0 International Public License. -->

```Bash
$ go version
go version go1.10 linux/amd64
```

# Chapter II: Interfaces

This chapter covers the inner workings of Go's interfaces.

Specifically, we'll look at:
- How functions & methods are called at run time.
- How interfaces are built and what they're made of.
- How, when and at what cost does dynamic dispatch work.
- How the empty interface & other special cases differ from their peers.
- How interface composition works.
- How and at what cost do type assertions work.

As we dig deeper and deeper, we'll also poke at miscellaneous low-level concerns, such as some implementation details of modern CPUs as well as various optimizations techniques used by the Go compiler.

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

- *This chapter assumes you're familiar with Go's assembler ([chapter I](../chapter1_assembly_primer/README.md)).*
- *If and when running into architecture-specific matters, always assume `linux/amd64`.*
- *We will always work with compiler optimizations **enabled**.*
- *Quoted text and/or comments always come from the official documentation (including Russ Cox "Function Calls" design document) and/or codebase, unless stated otherwise.*

## Function and method calls

As pointed out by Russ Cox in his design document about function calls (listed at the end of this chapter), Go has..:

..4 different kinds of functions..:
> - top-level func
> - method with value receiver
> - method with pointer receiver
> - func literal

..and 5 different kinds of calls:
> - direct call of top-level func (`func TopLevel(x int) {}`)
> - direct call of method with value receiver (`func (Value) M(int) {}`)
> - direct call of method with pointer receiver (`func (*Pointer) M(int) {}`)
> - indirect call of method on interface (`type Interface interface { M(int) }`)
> - indirect call of func value (`var literal = func(x int) {}`)

Mixed together, these make up for 10 possible combinations of function and call types:
> - direct call of top-level func /
> - direct call of method with value receiver /
> - direct call of method with pointer receiver /
> - indirect call of method on interface / containing value with value method
> - indirect call of method on interface / containing pointer with value method
> - indirect call of method on interface / containing pointer with pointer method
> - indirect call of func value / set to top-level func
> - indirect call of func value / set to value method
> - indirect call of func value / set to pointer method
> - indirect call of func value / set to func literal
>
> (A slash separates what is known at compile time from what is only found out at run time.)

We'll first take a few minutes to review the three kinds of direct calls, then we'll shift our focus towards interfaces and indirect method calls for the rest of this chapter.  
We won't cover function literals in this chapter, as doing so would first require us to become familiar with the mechanics of closures.. which we'll inevitably do, in due time.

### Overview of direct calls

Consider the following code ([direct_calls.go](./direct_calls.go)):
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

Let's have a quick look at the code generated for each of those 4 calls.

**Direct call of a top-level function**

Looking at the assembly output for `Add(10, 32)`:
```Assembly
0x0000 TEXT	"".main(SB), $40-0
  ;; ...omitted everything but the actual function call...
  0x0021 MOVQ	$137438953482, AX
  0x002b MOVQ	AX, (SP)
  0x002f CALL	"".Add(SB)
  ;; ...omitted everything but the actual function call...
```
We see that, as we already knew from chapter I, this translates into a direct jump to a global function symbol in the `.text` section, with the arguments and return values stored on the caller's stack-frame.  
It's as straightforward as it gets.

Russ Cox wraps it up as such in his document:
> Direct call of top-level func:
> A direct call of a top-level func passes all arguments on the stack, expecting results to occupy the successive stack positions.

**Direct call of a method with pointer receiver**

First things first, the receiver is initialized via `adder := Adder{id: 6754}`:
```Assembly
0x0034 MOVL	$6754, "".adder+28(SP)
```
*(The extra-space on our stack-frame was pre-allocated as part of the frame-pointer preamble, which we haven't shown here for conciseness.)*

Then comes the actual method call to `adder.AddPtr(10, 32)`:
```Assembly
0x0057 LEAQ	"".adder+28(SP), AX	;; move &adder to..
0x005c MOVQ	AX, (SP)		;; ..the top of the stack (argument #1)
0x0060 MOVQ	$137438953482, AX	;; move (32,10) to..
0x006a MOVQ	AX, 8(SP)		;; ..the top of the stack (arguments #3 & #2)
0x006f CALL	"".(*Adder).AddPtr(SB)
```

Looking at the assembly output, we can clearly see that a call to a method (whether it has a value or pointer receiver) is almost identical to a function call, the only difference being that the receiver is passed as first argument.  
In this case, we do so by loading the effective address (`LEAQ`) of `"".adder+28(SP)` at the top of the frame, so that argument #1 becomes `&adder` (if you're a bit confused regarding the semantics of `LEA` vs. `MOV`, you may want to have a look at the links at the end of this chapter for some pointers).

Note how the compiler encodes the type of the receiver and whether it's a value or pointer directly into the name of the symbol: `"".(*Adder).AddPtr`.

> Direct call of method:
> In order to use the same generated code for both an indirect call of a func value and for a direct call, the code generated for a method (both value and pointer receivers) is chosen to have the same calling convention as a top-level function with the receiver as a leading argument.

**Direct call of a method with value receiver**

As we'd expect, using a value receiver yields very similar code as above.  
Consider `adder.AddVal(10, 32)`:
```Assembly
0x003c MOVQ	$42949679714, AX	;; move (10,6754) to..
0x0046 MOVQ	AX, (SP)		;; ..the top of the stack (arguments #2 & #1)
0x004a MOVL	$32, 8(SP)		;; move 32 to the top of the stack (argument #3)
0x0052 CALL	"".Adder.AddVal(SB)
```

Looks like something a bit trickier is going on here, though: the generated assembly isn't even referencing `"".adder+28(SP)` anywhere, even though that is where our receiver currently resides.  
So what's really going on here? Well, since the receiver is a value, and since the compiler is able to statically infer that value, it doesn't bother with copying the existing value from its current location (`28(SP)`): instead, it simply creates a new, identical `Adder` value directly on the stack, and merges this operation with the creation of the second argument to save one more instruction in the process.

Once again, notice how the symbol name of the method explicitly denotes that it expects a value receiver.

### Implicit dereferencing

There's one final call that we haven't looked at yet: `(&adder).AddVal(10, 32)`.  
In that case, we're using a pointer variable to call a method that instead expects a value receiver. Somehow, Go automagically dereferences our pointer and manages to make the call. How so?

How the compiler handles this kind of situation depends on whether or not the receiver being pointed to has escaped to the heap or not.

**Case A: The receiver is on the stack**

If the receiver is still on the stack and its size is sufficiently small that it can be copied in a few instructions, as is the case here, the compiler simply copies its value over to the top of the stack then does a straightforward method call to `"".Adder.AddVal` (i.e. the one with a value receiver).

`(&adder).AddVal(10, 32)` thus looks like this in this situation:
```Assembly
0x0074 MOVL	"".adder+28(SP), AX	;; move (i.e. copy) adder (note the MOV instead of a LEA) to..
0x0078 MOVL	AX, (SP)		;; ..the top of the stack (argument #1)
0x007b MOVQ	$137438953482, AX	;; move (32,10) to..
0x0085 MOVQ	AX, 4(SP)		;; ..the top of the stack (arguments #3 & #2)
0x008a CALL	"".Adder.AddVal(SB)
```

Boring (although efficient). Let's move on to case B.

**Case B: The receiver is on the heap**

If the receiver has escaped to the heap then the compiler has to take a cleverer route: it generates a new method (with a pointer receiver, this time) that wraps `"".Adder.AddVal`, and replaces the original call to `"".Adder.AddVal` (the wrappee) with a call to `"".(*Adder).AddVal` (the wrapper).  
The wrapper's sole mission, then, is to make sure that the receiver gets properly dereferenced before being passed to the wrappee, and that any arguments and return values involved are properly copied back and forth between the caller and the wrappee.

(*NOTE: In assembly outputs, these wrapper methods are marked as `<autogenerated>`.*)

Here's an annotated listing of the generated wrapper that should hopefully clear things up a bit:
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

Obviously, this kind of wrapper can induce quite a bit of overhead considering all the copying that needs to be done in order to pass the arguments back and forth; especially if the wrappee is just a few instructions.  
Fortunately, in practice, the compiler would have inlined the wrappee directly into the wrapper to amortize these costs (when feasible, at least).

Note the `WRAPPER` directive in the definition of the symbol, which indicates that this method shouldn't appear in backtraces (so as not to confuse the end-user), nor should it be able to recover from panics that might be thrown by the wrappee.
> WRAPPER: This is a wrapper function and should not count as disabling recover.

The `runtime.panicwrap` function, which throws a panic if the wrapper's receiver is `nil`, is pretty self-explanatory; here's its complete listing for reference ([src/runtime/error.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/error.go#L132-L157)):
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

That's all for function and method calls, we'll now focus on the main course: interfaces.

## Anatomy of an interface

### Overview of the datastructures

Before we can understand how they work, we first need to build a mental model of the datastructures that make up interfaces and how they're laid out in memory.  
To that end, we'll have a quick peek into the runtime package to see what an interface actually looks like from the standpoint of the Go implementation.

**The `iface` structure**

`iface` is the root type that represents an interface within the runtime ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L143-L146)).  
Its definition goes like this:
```Go
type iface struct { // 16 bytes on a 64bit arch
    tab  *itab
    data unsafe.Pointer
}
```

An interface is thus a very simple structure that maintains 2 pointers:
- `tab` holds the address of an `itab` object, which embeds the datastructures that describe both the type of the interface as well as the type of the data it points to.
- `data` is a raw (i.e. `unsafe`) pointer to the value held by the interface.

While extremely simple, this definition already gives us some valuable information: since interfaces can only hold pointers, *any concrete value that we wrap into an interface will have to have its address taken*.  
More often than not, this will result in a heap allocation as the compiler takes the conservative route and forces the receiver to escape.  
This holds true even for scalar types!

We can prove that with a few lines of code ([escape.go](./escape.go)):
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

One could even visualize the resulting heap allocation using a simple benchmark ([escape_test.go](./escape_test.go)):
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

We can clearly see how each time we create a new `Addifier` interface and initialize it with our `adder` variable, a heap allocation of `sizeof(Adder)` actually takes place. 
Later in this chapter, we'll see how even simple scalar types can lead to heap allocations when used with interfaces.

Let's turn our attention towards the next datastructure: `itab`.

**The `itab` structure**

`itab` is defined thusly ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L648-L658)):
```Go
type itab struct { // 40 bytes on a 64bit arch
    inter *interfacetype
    _type *_type
    hash  uint32 // copy of _type.hash. Used for type switches.
    _     [4]byte
    fun   [1]uintptr // variable sized. fun[0]==0 means _type does not implement inter.
}
```

An `itab` is the heart & brain of an interface.  

First, it embeds a `_type`, which is the internal representation of any Go type within the runtime.  
A `_type` describes every facets of a type: its name, its characteristics (e.g. size, alignment...), and to some extent, even how it behaves (e.g. comparison, hashing...)!  
In this instance, the `_type` field describes the type of the value held by the interface, i.e. the value that the `data` pointer points to.

Second, we find a pointer to an `interfacetype`, which is merely a wrapper around `_type` with some extra information that are specific to interfaces.  
As you'd expect, the `inter` field describes the type of the interface itself.

Finally, the `fun` array holds the function pointers that make up the virtual/dispatch table of the interface.  
Notice the comment that says `// variable sized`, meaning that the size with which this array is declared is *irrelevant*.  
We'll see later in this chapter that the compiler is responsible for allocating the memory that backs this array, and does so independently of the size indicated here. Likewise, the runtime always accesses this array using raw pointers, thus bounds-checking does not apply here.

**The `_type` structure**

As we said above, the `_type` structure gives a complete description of a Go type.  
It's defined as such ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L25-L43)):
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

Thankfully, most of these fields are quite self-explanatory.

The `nameOff` & `typeOff` types are `int32` offsets into the metadata embedded into the final executable by the linker. This metadata is loaded into `runtime.moduledata` structures at run time ([src/runtime/symtab.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/symtab.go#L352-L393)), which should look fairly similar if you've ever had to look at the content of an ELF file.  
The runtime provide helpers that implement the necessary logic for following these offsets through the `moduledata` structures, such as e.g. `resolveNameOff` ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L168-L196)) and `resolveTypeOff` ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L202-L236)):
```Go
func resolveNameOff(ptrInModule unsafe.Pointer, off nameOff) name {}
func resolveTypeOff(ptrInModule unsafe.Pointer, off typeOff) *_type {}
```
I.e., assuming `t` is a `_type`, calling `resolveTypeOff(t, t.ptrToThis)` returns a copy of `t`.

**The `interfacetype` structure**

Finally, here's the `interfacetype` structure ([src/runtime/type.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/type.go#L342-L346)):
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

As mentioned, an `interfacetype` is just a wrapper around a `_type` with some extra interface-specific metadata added on top.  
In the current implementation, this metadata is mostly composed of a list of offsets that points to the respective names and types of the methods exposed by the interface (`[]imethod`).

**Conclusion**

Here's an overview of what an `iface` looks like when represented with all of its sub-types inlined; this hopefully should help connect all the dots:
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

This section glossed over the different data-types that make up an interface to help us to start building a mental model of the various cogs involved in the overall machinery, and how they all work with each other.  
In the next section, we'll learn how these datastructures actually get computed.

### Creating an interface

Now that we've had a quick look at all the datastructures involved, we'll focus on how they actually get allocated and initiliazed.

Consider the following program ([iface.go](./iface.go)):
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

*NOTE: For the remainder of this chapter, we will denote an interface `I` that holds a type `T` as `<I,T>`. E.g. `Mather(Adder{id: 6754})` instantiates an `iface<Mather, Adder>`.*

Let's zoom in on the instantiation of `iface<Mather, Adder>`:
```Go
m := Mather(Adder{id: 6754})
```
This single line of Go code actually sets off quite a bit of machinery, as the assembly listing generated by the compiler can attest:  
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

As you can see, we've splitted the output into three logical parts.

**Part 1: Allocate the receiver**

```Assembly
0x001d MOVL	$6754, ""..autotmp_1+36(SP)
```

A constant decimal value of `6754`, corresponding to the ID of our `Adder`, is stored at the beginning of the current stack-frame.  
It's stored there so that the compiler will later be able to reference it by its address; we'll see why in part 3.

**Part 2: Set up the itab**

```Assembly
0x0025 LEAQ	go.itab."".Adder,"".Mather(SB), AX
0x002c MOVQ	AX, (SP)
```

It looks like the compiler has already created the necessary `itab` for representing our `iface<Mather, Adder>` interface, and made it available to us via a global symbol: `go.itab."".Adder,"".Mather`.  

We're in the process of building an `iface<Mather, Adder>` interface and, in order to do so, we're loading the effective address of this global `go.itab."".Adder,"".Mather` symbol at the top of the current stack-frame.  
Once again, we'll see why in part 3.

Semantically, this gives us something along the lines of the following pseudo-code:
```Go
tab := getSymAddr(`go.itab.main.Adder,main.Mather`).(*itab)
```
That's half of our interface right there!

Now, while we're at it, let's have a deeper look at that `go.itab."".Adder,"".Mather` symbol.  
As usual, the `-S` flag of the compiler can tell us a lot:
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

Neat. Let's analyze this piece by piece.

The first piece declares the symbol and its attributes:
```
go.itab."".Adder,"".Mather SRODATA dupok size=40
```
As usual, since we're looking directly at the intermediate object file generated by the compiler (i.e. the linker hasn't run yet), symbol names are still missing package names. Nothing new on that front.  
Other than that, what we've got here is a 40-byte global object symbol that will be stored in the `.rodata` section of our binary.

Note the `dupok` directive, which tells the linker that it is legal for this symbol to appear multiple times at link-time: the linker will have to arbitrarily choose one of them over the others.  
What makes the Go authors think that this symbol might end up duplicated, I'm not sure. Feel free to file an issue if you know more.  
*[UPDATE: We've discussed about this matter in [issue #7: How you can get duplicated go.itab interface definitions](https://github.com/teh-cmc/go-internals/issues/7).]*

The second piece is a hexdump of the 40 bytes of data associated with the symbol. I.e., it's a serialized representation of an `itab` structure:
```
0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
0x0010 8a 3d 5f 61 00 00 00 00 00 00 00 00 00 00 00 00  .=_a............
0x0020 00 00 00 00 00 00 00 00                          ........
```
As you can see, most of this data is just a bunch of zeros at this point. The linker will take care of filling them up, as we'll see in a minute.

Notice how, among all these zeros, 4 bytes actually have been set though, at offset `0x10+4`.  
If we take a look back at the declaration of the `itab` structure and annotate the respective offsets of its fields:
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
We see that offset `0x10+4` matches the `hash uint32` field: i.e., the hash value that corresponds to our `main.Adder` type is already right there in our object file.

The third and final piece lists a bunch of relocation directives for the linker:
```
rel 0+8 t=1 type."".Mather+0
rel 8+8 t=1 type."".Adder+0
rel 24+8 t=1 "".(*Adder).Add+0
rel 32+8 t=1 "".(*Adder).Sub+0
```

`rel 0+8 t=1 type."".Mather+0` tells the linker to fill up the first 8 bytes (`0+8`) of the contents with the address of the global object symbol `type."".Mather`.  
`rel 8+8 t=1 type."".Adder+0` then fills the next 8 bytes with the address of `type."".Adder`, and so on and so forth.

Once the linker has done its job and followed all of these directives, our 40-byte serialized `itab` will be complete.  
Overall, we're now looking at something akin to the following pseudo-code:
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

We've got ourselves a ready-to-use `itab`, now if we just had some data to along with it, that'd make for a nice, complete interface.

**Part 3: Set up the data**

```Assembly
0x0030 LEAQ	""..autotmp_1+36(SP), AX
0x0035 MOVQ	AX, 8(SP)
0x003a CALL	runtime.convT2I32(SB)
0x003f MOVQ	16(SP), AX
0x0044 MOVQ	24(SP), CX
```

Remember from part 2 that the top of the stack `(SP)` currently holds the address of `go.itab."".Adder,"".Mather` (argument #1).  
Also remember from part 1 that we had stored a `$6754` decimal constant in `""..autotmp_1+36(SP)`: we now load the effective address of this constant just below the top of the stack-frame, at 8(SP) (argument #2).

These two pointers are the two arguments that we pass into `runtime.convT2I32`, which will apply the final touches of glue to create and return our complete interface.  
Let's have a closer look at it ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L433-L451)):
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

So `runtime.convT2I32` does 4 things:
1. It creates a new `iface` structure `i` (to be pedantic, its caller creates it.. same difference).
2. It assigns the `itab` pointer we just gave it to `i.tab`.
3. It **allocates a new object of type `i.tab._type` on the heap**, then copy the value pointed to by the second argument `elem` into that new object.
4. It returns the final interface.

This process is quite straightforward overall, although the 3rd step does involve some tricky implementation details in this specific case, which are caused by the fact that our `Adder` type is effectively a scalar type.  
We'll look at the interactions of scalar types and interfaces in more details in the section about [the special cases of interfaces](#interface-holding-a-scalar-type).

Conceptually, we've now accomplished the following (pseudo-code):
```Go
tab := getSymAddr(`go.itab.main.Adder,main.Mather`).(*itab)
elem := getSymAddr(`""..autotmp_1+36(SP)`).(*int32)

i := runtime.convTI32(tab, unsafe.Pointer(elem))

assert(i.tab == tab)
assert(*(*int32)(i.data) == 6754) // same value..
assert((*int32)(i.data) != elem)  // ..but different (al)locations!
```

To summarize all that just went down, here's a complete, annotated version of the assembly code for all 3 parts:
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
Keep in mind that all of this started with just one single line: `m := Mather(Adder{id: 6754})`.

We finally got ourselves a complete, working interface.

### Reconstructing an `itab` from an executable

In the previous section, we dumped the contents of `go.itab."".Adder,"".Mather` directly from the object files generated by the compiler and ended up looking at what was mostly a blob of zeros (except for the `hash` value):
```
$ GOOS=linux GOARCH=amd64 go tool compile -S iface.go | grep -A 3 '^go.itab."".Adder,"".Mather'
go.itab."".Adder,"".Mather SRODATA dupok size=40
    0x0000 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00  ................
    0x0010 8a 3d 5f 61 00 00 00 00 00 00 00 00 00 00 00 00  .=_a............
    0x0020 00 00 00 00 00 00 00 00                          ........
```

To get a better picture of how the data is laid out into the final executable produced by the linker, we'll walk through the generated ELF file and manually reconstruct the bytes that make up the `itab` of our `iface<Mather, Adder>`.  
Hopefully, this'll enable us to observe what our `itab` looks like once the linker has done its job.

First things first, let's build the `iface` binary: `GOOS=linux GOARCH=amd64 go build -o iface.bin iface.go`.

**Step 1: Find `.rodata`**

Let's print the section headers in search of `.rodata`, `readelf` can help with that:
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
What we really need here is the (decimal) offset of the section, so let's apply some pipe-foo:
```Bash
$ readelf -St -W iface.bin | \
  grep -A 1 .rodata | \
  tail -n +2 | \
  awk '{print "ibase=16;"toupper($3)}' | \
  bc
315392
```

Which means that `fseek`-ing 315392 bytes into our binary should place us right at the start of the `.rodata` section.  
Now what we need to do is map this file location to a virtual-memory address.

**Step 2: Find the virtual-memory address (VMA) of `.rodata`**

The VMA is the virtual address at which the section will be mapped once the binary has been loaded in memory by the OS. I.e., this is the address that we'll use to reference a symbol at runtime.

The reason we care about the VMA in this case is that we cannot directly ask `readelf` or `objdump` for the offset of a specific symbol (AFAIK). What we can do, on the other hand, is ask for the VMA of a specific symbol.  
Coupled with some simple maths, we should be able to build a mapping between VMAs and offsets and finally find the offsets of the symbols that we're looking for.

Finding the VMA of `.rodata` is no different than finding its offset, it's just a different column is all:
```Bash
$ readelf -St -W iface.bin | \
  grep -A 1 .rodata | \
  tail -n +2 | \
  awk '{print "ibase=16;"toupper($2)}' | \
  bc
4509696
```

So here's what we know so far: the `.rodata` section is located at offset `$315392` (= `0x04d000`) into the ELF file, which will be mapped at virtual address `$4509696` (= `0x44d000`) at run time.

Now we need the VMA as well as the size of the symbol we're looking for:
- Its VMA will (indirectly) allow us to locate it within the executable.
- Its size will tell us how much data to extract once we've found the correct offset.

**Step 3: Find the VMA & size of `go.itab.main.Adder,main.Mather`**

`objdump` has those available for us.

First, find the symbol:
```Bash
$ objdump -t -j .rodata iface.bin | grep "go.itab.main.Adder,main.Mather"
0000000000475140 g     O .rodata	0000000000000028 go.itab.main.Adder,main.Mather
```

Then, get its VMA in decimal form:
```Bash
$ objdump -t -j .rodata iface.bin | \
  grep "go.itab.main.Adder,main.Mather" | \
  awk '{print "ibase=16;"toupper($1)}' | \
  bc
4673856
```

And finally, get its size in decimal form:
```Bash
$ objdump -t -j .rodata iface.bin | \
  grep "go.itab.main.Adder,main.Mather" | \
  awk '{print "ibase=16;"toupper($5)}' | \
  bc
40
```

So `go.itab.main.Adder,main.Mather` will be mapped at virtual address `$4673856` (= `0x475140`) at run time, and has a size of 40 bytes (which we already knew, as it's the size of an `itab` structure).

**Step 4: Find & extract `go.itab.main.Adder,main.Mather`**

We now have all the elements we need in order to locate `go.itab.main.Adder,main.Mather` within our binary.  

Here's a reminder of what we know so far:
```
.rodata offset: 0x04d000 == $315392
.rodata VMA: 0x44d000 == $4509696

go.itab.main.Adder,main.Mather VMA: 0x475140 == $4673856
go.itab.main.Adder,main.Mather size: 0x24 = $40
```

If `$315392` (`.rodata`'s offset) maps to $4509696 (`.rodata`'s VMA) and `go.itab.main.Adder,main.Mather`'s VMA is `$4673856`, then `go.itab.main.Adder,main.Mather`'s offset within the executable is:  
`sym.offset = sym.vma - section.vma + section.offset = $4673856 - $4509696 + $315392 = $479552`.

Now that we know both the offset and size of the data, we can take out good ol' `dd` and extract the raw bytes straight out of the executable:  
```Bash
$ dd if=iface.bin of=/dev/stdout bs=1 count=40 skip=479552 2>/dev/null | hexdump
0000000 bd20 0045 0000 0000 ed40 0045 0000 0000
0000010 3d8a 615f 0000 0000 c2d0 0044 0000 0000
0000020 c350 0044 0000 0000                    
0000028
```

This certainly does look like a clear-cut victory.. but is it, really? Maybe we've just dumped 40 totally random, unrelated bytes? Who knows?  
There's at least one way to be sure: let's compare the type hash found in our binary dump (at offset `0x10+4` -> `0x615f3d8a`) with the one loaded by the runtime ([iface_type_hash.go](./iface_type_hash.go)):
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

It's a match! `fmt.Printf("iface.tab.hash = %#x\n", iface.tab.hash)` gives us `0x615f3d8a`, which corresponds to the value that we've extracted from the contents of the ELF file.

**Conclusion**

We've reconstructed the complete `itab` for our `iface<Mather, Adder>` interface; it's all there in the executable, just waiting to be used, and already contains all the information that the runtime will need to make the interface behave as we expect.

Of course, since an `itab` is mostly composed of a bunch of pointers to other datastructures, we'd have to follow the virtual addresses present in the contents that we've extracted via `dd` in order to reconstruct the complete picture.  
Speaking of pointers, we can now have a clear view of the virtual-table for `iface<Mather, Adder>`; here's an annotated version of the contents of `go.itab.main.Adder,main.Mather`:
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

Without surprise, the virtual table for `iface<Mather, Adder>` holds two method pointers: `main.(*Adder).add` and `main.(*Adder).sub`.  
Well, actually, this *is* a bit surprising: we've never defined these two methods to have pointer receivers.  
The compiler has generated these wrapper methods on our behalf (as we've described in the ["Implicit dereferencing" section](#implicit-dereferencing)) because it knows that we're going to need them: since an interface can only hold pointers, and since our `Adder` implementation of said interface only provides methods with value-receivers, we'll have to go through a wrapper at some point if we're going to call either of these methods via the virtual table of the interface.

This should already give you a pretty good idea of how dynamic dispatch is handled at run time; which is what we will look at in the next section.

**Bonus**

I've hacked up a generic bash script that you can use to dump the contents of any symbol in any section of an ELF file ([dump_sym.sh](./dump_sym.sh)):
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

I'd imagine there must exist an easier way to do what this script does, maybe some arcane flags or an obscure gem hidden inside the `binutils` distribution.. who knows.  
If you've got some hints, don't hesitate to say so in the issues.

## Dynamic dispatch

In this section we'll finally cover the main feature of interfaces: dynamic dispatch.  
Specifically, we'll look at how dynamic dispatch works under the hood, and how much we got to pay for it.

### Indirect method call on interface

Let's have a look back at our code from earlier ([iface.go](./iface.go)):
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

We've already had a deeper look into most of what happens in this piece of code: how the `iface<Mather, Adder>` interface gets created, how it's laid out in the final exectutable, and how it ends up being loaded by the runtime.  
There's only one thing left for us to look at, and that is the actual indirect method call that follows: `m.Add(10, 32)`.

To refresh our memory, we'll zoom in on both the creation of the interface as well as on the method call itself:
```Go
m := Mather(Adder{id: 6754})
m.Add(10, 32)
```
Thankfully, we already have a fully annotated version of the assembly generated by the instantiation done on the first line (`m := Mather(Adder{id: 6754})`):
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
And now, here's the assembly listing for the indirect method call that follows (`m.Add(10, 32)`):
```Assembly
;; m.Add(10, 32)
0x0049 MOVQ	24(AX), AX
0x004d MOVQ	$137438953482, DX
0x0057 MOVQ	DX, 8(SP)
0x005c MOVQ	CX, (SP)
0x0060 CALL	AX
```

With the knowledge accumulated from the previous sections, these few instructions should be straightforward to understand.

```Assembly
0x0049 MOVQ	24(AX), AX
```
Once `runtime.convT2I32` has returned, `AX` holds `i.tab`, which as we know is a pointer to an `itab`; and more specifically a pointer to `go.itab."".Adder,"".Mather` in this case.  
By dereferencing `AX` and offsetting 24 bytes forward, we reach `i.tab.fun`, which corresponds to the first entry of the virtual table.  
Here's a reminder of what the offset table for `itab` looks like:
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

As we've seen in the previous section where we've reconstructed the final `itab` directly from the executable, `iface.tab.fun[0]` is a pointer to `main.(*Adder).add`, which is the compiler-generated wrapper-method that wraps our original value-receiver `main.Adder.add` method.

```Assembly
0x004d MOVQ	$137438953482, DX
0x0057 MOVQ	DX, 8(SP)
```
We store `10` and `32` at the top of the stack, as arguments #2 & #3.

```Assembly
0x005c MOVQ	CX, (SP)
0x0060 CALL	AX
```
Once `runtime.convT2I32` has returned, `CX` holds `i.data`, which is a pointer to our `Adder` instance.  
We move this pointer to the top of stack, as argument #1, to satisfy the calling convention: the receiver for a method should always be passed as the first argument.

Finally, with our stack all set up, we can do the actual call.

We'll close this section with a complete annotated assembly listing of the entire process:
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

We now have a clear picture of the entire machinery required for interfaces and virtual method calls to work.  
In the next section, we'll measure the actual cost of this machinery, in theory as well as in practice.

### Overhead

As we've seen, the implementation of interfaces delegates most of the work on both the compiler and the linker. From a performance standpoint, this is obviously very good news: we effectively want to relieve the runtime from as much work as possible.  
There do exist some specific cases where instantiating an interface may also require the runtime to get to work (e.g. the `runtime.convT2*` family of functions), though they are not so prevalent in practice.  
We'll learn more about these edge cases in the [section dedicated to the special cases of interfaces](#special-cases--compiler-tricks). In the meantime, we'll concentrate purely on the overhead of virtual method calls and ignore the one-time costs related to instantiation.

Once an interface has been properly instantiated, calling methods on it is nothing more than going through one more layer of indirection compared to the usual statically dispatched call (i.e. dereferencing `itab.fun` at the desired index).  
As such, one would imagine this process to be virtually free.. and one would be kind of right, but not quite: the theory is a bit tricky, and the reality even trickier still.

#### The theory: quick refresher on modern CPUs

The extra indirection inherent to virtual calls is, in and of itself, effectively free *for as long as it is somewhat predictable from the standpoint of the CPU*.  
Modern CPUs are very aggressive beasts: they cache aggressively, they aggressively pre-fetch both instructions & data, they pre-execute code aggressively, they even reorder and parallelize it as they see fit.  
All of this extra work is done whether we want it or not, hence we should always strive not to get in the way of the CPU's efforts to be extra smart, so all of these precious cycles don't go needlessly wasted.

This is where virtual method calls can quickly become a problem.

In the case of a statically dispatched call, the CPU has foreknowledge of the upcoming branch in the program and pre-fetches the necessary instructions accordingly. This makes up for a smooth, transparent transition from one branch of the program to the other as far as performance is concerned.  
With dynamic dispatch, on the other hand, the CPU cannot know in advance where the program is heading: it all depends on computations whose results are, by definition, not known until run time. To counter-balance this, the CPU applies various algorithms and heuristics in order to guess where the program is going to branch next (i.e. "branch prediction").

If the processor guesses correctly, we can expect a dynamic branch to be almost as efficient as a static one, since the instructions of the landing site have already been pre-fetched into the processor's caches anyway.

If it gets things wrong, though, things can get a bit rough: first, of course, we'll have to pay for the extra indirection plus the corresponding (slow) load from main memory (i.e. the CPU is effectively stalled) to load the right instructions into the L1i cache. Even worse, we'll have to pay for the price of the CPU backtracking in its own mistakes and flushing its instruction pipeline following the branch misprediction.  
Another important downside of dynamic dispatch is that it makes inlining impossible by definition: one simply cannot inline what they don't know is coming.

All in all, it should, at least in theory, be very possible to end up with massive differences in performance between a direct call to an inlined function F, and a call to that same function that couldn't be inlined and had to go through some extra layers of indirection, and maybe even got hit by a branch misprediction on its way.

That's mostly it for the theory.  
When it comes to modern hardware, though, one should always be wary of the theory.

Let's measure this stuff.

#### The practice: benchmarks

First things first, some information about the CPU we're running on:
```Bash
$ lscpu | sed -nr '/Model name/ s/.*:\s*(.* @ .*)/\1/p'
Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz
```

We'll define the interface used for our benchmarks as such ([iface_bench_test.go](./iface_bench_test.go)):
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

**Benchmark suite A: single instance, many calls, inlined & non-inlined**

For our first two benchmarks, we'll try calling a non-inlined method inside a busy-loop, on both an `*Adder` value and a `iface<Mather, *Adder>` interface:
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

We expect both benchmarks to run A) extremely fast and B) at almost the same speeds.

Given the tightness of the loop, we can expect both benchmarks to have their data (receiver & vtable) and instructions (`"".(*id32).idNoInline`) already be present in the L1d/L1i caches of the CPU for each iteration of the loop. I.e., performance should be purely CPU-bound.

`BenchmarkMethodCall_interface` should run a bit slower (on the nanosecond scale) though, as it has to deal with the overhead of finding & copying the right pointer from the virtual table (which is already in the L1 cache, though).  
Since the `CALL CX` instruction has a strong dependency on the output of these few extra instructions required to consult the vtable, the processor has no choice but to execute all of this extra logic as a sequential stream, leaving any chance of instruction-level parallelization on the table.  
This is ultimately the main reason why we would expect the "interface" version to run a bit slower.

We end up with the following results for the "direct" version:
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
And here's for the "interface" version:
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

The results match our expectations: the "interface" version is indeed a bit slower, with approximately 0.15 extra nanoseconds per iteration, or a ~8% slowdown.  
8% might sound like a noticeable difference at first, but we have to keep in mind that A) these are nanosecond-scale measurements and B) the method being called does so little work that it magnifies even more the overhead of the call.

Looking at the number of instructions per benchmark, we see that the interface-based version has had to execute for ~14 billion more instructions compared to the "direct" version (`110,979,100,648` vs. `124,467,105,161`), even though both benchmarks were run for `6,000,000,000` (`2,000,000,000\*3`) iterations.  
As we've mentioned before, the CPU cannot parallelize these extra instructions due to the `CALL` depending on them, which gets reflected quite clearly in the instruction-per-cycle ratio: both benchmarks end up with a similar IPC ratio (`2.54` vs. `2.63`) even though the "interface" version has much more work to do overall.  
This lack of parallelism piles up to an extra ~3.5 billion CPU cycles for the "interface" version, which is where those extra 0.15ns that we've measured are actually spent.

Now what happens when we let the compiler inline the method call?

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

Two things jump out at us:
- `BenchmarkMethodCall_direct`: Thanks to inlining, the call has been reduced to a simple pair of memory moves.
- `BenchmarkMethodCall_interface`: Due to dynamic dispatch, the compiler has been unable to inline the call, thus the generated assembly ends up being exactly the same as before.

We won't even bother running `BenchmarkMethodCall_interface` since the code hasn't changed a bit.  
Let's have a quick look at the "direct" version though:
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

As expected, this runs ridiculously fast now that the overhead of the call is gone.  
With ~0.34ns per op for the "direct" inlined version, the "interface" version is now ~475% slower, quite a steep drop from the ~8% difference that we've measured earlier with inlining disabled.

Notice how, with the branching inherent to the method call now gone, the CPU is able to parallelize and speculatively execute the remaining instructions much more efficiently, reaching an IPC ratio of 4.61.

**Benchmark suite B: many instances, many non-inlined calls, small/big/pseudo-random iterations**

For this second benchmark suite, we'll look at a more real-world-like situation in which an iterator goes through a slice of objects that all expose a common method and calls it for each object.  
To better mimic reality, we'll disable inlining, as most methods called this way in a real program would most likely by sufficiently complex not to be inlined by the compiler (YMMV; a good counter-example of this is the `sort.Interface` interface from the standard library).

We'll define 3 similar benchmarks that just differ in the way they access this slice of objects; the goal being to simulate decreasing levels of cache friendliness:
1. In the first case, the iterator walks the array in order, calls the method, then gets incremented by the size of one object for each iteration.
1. In the second case, the iterator still walks the slice in order, but this time gets incremented by a value that's larger than the size of a single cache-line.
1. Finally, in the third case, the iterator will pseudo-randomly steps through the slice.

In all three cases, we'll make sure that the array is big enough not to fit entirely in any of the processor's caches in order to simulate (not-so-accurately) a very busy server that's putting a lot of pressure of both its CPU caches and main memory.

Here's a quick recap of the processor's attributes, we'll design the benchmarks accordingly:
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

Here's what the benchmark suite looks like for the "direct" version (the benchmarks marked as `baseline` compute the cost of retrieving the receiver in isolation, so that we can subtract that cost from the final measurements):
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
The benchmark suite for the "interface" version is identical, except that the array is initialized with interface values instead of pointers to the concrete type, as one would expect:
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

For the "direct" suite, we get the following results:
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
There really are no surprises here:
1. `small_incr`: By being *extremely* cache-friendly, we obtain results similar to the previous benchmark that looped over a single instance.
2. `big_incr`: By forcing the CPU to fetch a new cache-line at every iteration, we do see a noticeable bump in latencies, which is completely unrelated to the cost of doing the call, though: ~6ns are attributable to the baseline while the rest is a combination of the cost of dereferencing the receiver in order to get to its `id` field and copying around the return value.
3. `random_incr`: Same remarks as with `big_incr`, except that the bump in latencies is even more pronounced due to A) the pseudo-random accesses and B) the cost of retrieving the next index from the pre-computed array of indexes (which triggers cache misses in and of itself).

As logic would dictate, thrashing the CPU d-caches doesn't seem to influence the latency of the actual direct method call (inlined or not) by any mean, although it does make everything that surrounds it slower.

What about dynamic dispatch?
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
The results are extremely similar, albeit a tiny bit slower overall simply due to the fact that we're copying two quad-words (i.e. both fields of an `identifier` interface) out of the slice at each iteration instead of one (a pointer to `id32`).

The reason this runs almost as fast as its "direct" counterpart is that, since all the interfaces in the slice share a common `itab` (i.e. they're all `iface<Mather, Adder>` interfaces), their associated virtual table never leaves the L1d cache and so fetching the right method pointer at each iteration is virtually free.  
Likewise, the instructions that make up the body of the `main.(*id32).idNoInline` method never leave the L1i cache.

One might think that, in practice, a slice of interfaces would encompass many different underlying types (and thus vtables), which would result in thrashing of both the L1i and L1d caches due to the varying vtables pushing each other out.  
While that holds true in theory, these kinds of thoughts tend to be the result of years of experience using older OOP languages such as C++ that (used to, at least) encourage the use of deeply-nested hierarchies of inherited classes and virtual calls as their main tool of abstraction.  
With big enough hierarchies, the number of associated vtables could sometimes get large enough to thrash the CPU caches when iterating over a datastructure holding various implementations of a virtual class (think e.g. of a GUI framework where everything is a `Widget` stored in a graph-like datastructure); especially so that, in C++ at least, virtual classes tend to specify quite complex behaviors, sometimes with dozen of methods, resulting in quite big vtables and even more pressure on the L1d cache.

Go, on the other hand, has very different idioms: OOP has been completely thrown out of the window, the type system flattened, and interfaces are most often used to describe minimal, constrained behaviors (a few methods at most an average, helped by the fact that interfaces are implicitly satisfied) instead of being used as an abstraction on top of a more complex, layered type hierarchy.  
In practice, in Go, I've found it's very rare to have to iterate over a set of interfaces that carry many different underlying types. YMMV, of course.

For the curious-minded, here's what the results of the "direct" version would have looked like with inlining enabled:
```Bash
name                                                time/op
MethodCall_direct/many/inline/small_incr            0.97ns ± 1% # 0.97ns
MethodCall_direct/many/inline/big_incr/baseline     5.96ns ± 1%
MethodCall_direct/many/inline/big_incr/call         11.9ns ± 1% # 11.9 - 5.96 = 5.94ns
MethodCall_direct/many/inline/random_incr/baseline  9.20ns ± 1%
MethodCall_direct/many/inline/random_incr/call      16.9ns ± 1% # 16.9 - 9.2 = 7.7ns
```
Which would have made the "direct" version around 2 to 3 times faster than the "interface" version in cases where the compiler would have been able to inline the call.  
Then again, as we've mentioned earlier, the limited capabilities of the current compiler with regards to inlining mean that, in practice, these kind of wins would rarely be seen. And of course, there often are times when you really don't have a choice but to resort to virtual calls anyway.

**Conclusion**

Effectively measuring the latency of a virtual call turned out to be quite a complex endeavor, as most of it is the direct consequence of many intertwined side-effects that result from the very complex implementation details of modern hardware.

*In Go*, thanks to the idioms encouraged by the design of the language, and taking into account the (current) limitations of the compiler with regards to inlining, one could effectively consider dynamic dispatch as virtually free.  
Still, when in doubt, one should always measure their hot paths and look at the relevant performance counters to assert with certainty whether dynamic dispatch ends up being an issue or not.

*(NOTE: We will look at the inlining capabilities of the compiler in a later chapter of this book.*)

## Special cases & compiler tricks

This section will review some of the most common special cases that we encounter every day when dealing with interfaces.

By now you should have a pretty clear idea of how interfaces work, so we'll try and aim for conciseness here.

### The empty interface

The datastructure for the empty interface is what you'd intuitively think it would be: an `iface` without an `itab`.  
There are two reasons for that:
1. Since the empty interface has no methods, everything related to dynamic dispatch can safely be dropped from the datastructure.
1. With the virtual table gone, the type of the empty interface itself, not to be confused with the type of the data it holds, is always the same (i.e. we talk about *the* empty interface rather than *an* empty interface).

*NOTE: Similar to the notation we used for `iface`, we'll denote the empty interface holding a type T as `eface<T>`*

`eface` is the root type that represents the empty interface within the runtime ([src/runtime/runtime2.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/runtime2.go#L148-L151)).  
Its definition goes like this:
```Go
type eface struct { // 16 bytes on a 64bit arch
    _type *_type
    data  unsafe.Pointer
}
```
Where `_type` holds the type information of the value pointed to by `data`.  
As expected, the `itab` has been dropped entirely.

While the empty interface could just reuse the `iface` datastructure (it is a superset of `eface` after all), the runtime chooses to distinguish the two for two main reasons: space efficiency and code clarity.

### Interface holding a scalar type

Earlier in this chapter ([#Anatomy of an Interface](#overview-of-the-datastructures)), we've mentioned that even storing a simple scalar type such as an integer into an interface will result in a heap allocation.  
It's time we see why, and how.

Consider these two benchmarks ([eface_scalar_test.go](./eface_scalar_test.go)):
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
1. That's a 2-orders-of-magnitude difference in performance for a simple assignment operation, and
1. we can see that the second benchmark has to allocate 4 extra bytes at each iteration.

Clearly, some hidden heavy machinery is being set off in the second case: we need to have a look at the generated assembly.

For the first benchmark, the compiler generates exactly what you'd expect it to with regard to the assignment operation:
```Assembly
;; Uint = uint32(i)
0x000d MOVL	DX, (AX)
```

In the second benchmark, though, things get far more complex:
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
This is *just* the assignment, not the complete benchmark!  
We'll have to study this code piece by piece.

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

This first piece of the listing instantiates the empty interface `eface<uint32>` that we will later assign to `Eface`.

We've already studied similar code in the section about creating interfaces ([#Creating an interface](#creating-an-interface)), except that this code was calling `runtime.convT2I32` instead of `runtime.convT2E32` here; nonetheless, this should look very familiar.

It turns out that `runtime.convT2I32` and `runtime.convT2E32` are part of a larger family of functions whose job is to instanciate either a specific interface or the empty interface from a scalar value (or a string or slice, as special cases).  
This family is composed of 10 symbols, one for each combination of `(eface/iface, 16/32/64/string/slice)`:
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
(*You'll notice that there is no `convT2E8` nor `convT2I8` function; this is due to a compiler optimization that we'll take a look at at the end of this section.*)

All of these functions do almost the exact same thing, they only differ in the type of their return value (`iface` vs. `eface`) and the size of the memory that they allocate on the heap.  
Let's take a look at e.g. `runtime.convT2E32` more closely ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L308-L325)):
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

The function initializes the `_type` field of the `eface` structure "passed" in by the caller (remember: return values are allocated by the caller on its own stack-frame) with the `_type` given as first parameter.  
For the `data` field of the `eface`, it all depends on the value of the second parameter `elem`:
- If `elem` is zero, `e.data` is initialized to point to `runtime.zeroVal`, which is a special global variable defined by the runtime that represents the zero value. We'll discuss a bit more about this special variable in the next section.
- If `elem` is non-zero, the function allocates 4 bytes on the heap (`x = mallocgc(4, t, false)`), initializes the contents of those 4 bytes with the value pointed to by `elem` (`*(*uint32)(x) = *(*uint32)(elem)`), then stick the resulting pointer into `e.data`.

In this case, `e._type` holds the address of `type.uint32` (`LEAQ type.uint32(SB), AX`), which is implemented by the standard library and whose address will only be known when linking against said stdlib:
```Bash
$ go tool nm eface_scalar_test.o | grep 'type\.uint32'
         U type.uint32
```
(`U` denotes that the symbol is not defined in this object file, and will (hopefully) be provided by another object at link-time (i.e. the standard library in this case).)

**Step 2: Assign the result (part 1)**

```Assembly
0x0078 MOVQ	"".&Eface+48(SP), DX
0x007d MOVQ	CX, (DX)		;; Eface._type = ret._type
```

The result of `runtime.convT2E32` gets assigned to our `Eface` variable.. or does it?

Actually, for now, only the `_type` field of the returned value is being assigned to `Eface._type`, the `data` field cannot be copied over just yet.

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

The apparent complexity of this last piece is a side-effect of assigning the `data` pointer of the returned `eface` to `Eface.data`: since we're manipulating the memory graph of our program (i.e. which part of memory holds references to which part of memory), we may have to notify the garbage collector of this change, just in case a garbage collection were to be currently running in the background.

This is known as a write barrier, and is a direct consequence of Go's *concurrent* garbage collector.  
Don't worry if this sounds a bit vague for now; the next chapter of this book will offer a thorough review of garbage collection in Go.  
For now, it's enough to remember that when we see some assembly code calling into `runtime.gcWriteBarrier`, it has to do with pointer manipulation and notifying the garbage collector.

All in all, this final piece of code can do one of two things:
- If the write-barrier is currently inactive, it assigns `ret.data` to `Eface.data` (`MOVQ AX, 8(DX)`).
- If the write-barrier is active, it politely asks the garbage-collector to do the assignment on our behalf  
(`LEAQ 8(DX), DI` + `CALL runtime.gcWriteBarrier(SB)`).

(*Once again, try not to worry too much about this for now.*)

Voila, we've got a complete interface holding a simple scalar type (`uint32`).

**Conclusion**

While sticking a scalar value into an interface is not something that happens that often in practice, it can be a costly operation for various reasons, and as such it's important to be aware of the machinery behind it.

Speaking of cost, we've mentioned that the compiler implements various tricks to avoid allocating in some specific situations; we'll close this section with a quick look at 3 of those tricks.

**Interface trick 1: Byte-sized values**

Consider this benchmark that instanciates an `eface<uint8>` ([eface_scalar_test.go](./eface_scalar_test.go)):
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

We notice that in the case of a byte-sized value, the compiler avoids the call to `runtime.convT2E`/`runtime.convT2I` and the associated heap allocation, and instead re-uses the address of a global variable exposed by the runtime that already holds the 1-byte value we're looking for: `LEAQ    runtime.staticbytes(SB), R8`.

`runtime.staticbytes` can be found in [src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L619-L653) and looks like this:
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
Using the right offset into this array, the compiler can effectively avoid an extra heap allocation and still reference any value representable as a single byte.

Something feels wrong here, though.. can you tell?  
The generated code still embeds all the machinery related to the write-barrier, even though the pointer we're manipulating holds the address of a global variable whose lifetime is the same as the entire program's anyway.  
I.e. `runtime.staticbytes` can never be garbage collected, no matter which part of memory holds a reference to it or not, so we shouldn't have to pay for the overhead of a write-barrier in this case.

**Interface trick 2: Static inference**

Consider this benchmark that instanciates an `eface<uint64>` from a value known at compile time ([eface_scalar_test.go](./eface_scalar_test.go)):
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

We can see from the generated assembly that the compiler completely optimizes out the call to `runtime.convT2E64`, and instead directly constructs the empty interface by loading the address of an autogenerated global variable that already holds the value we're looking for: `LEAQ "".statictmp_0(SB), SI` (note the `(SB)` part, indicating a global variable).

We can better visualize what's going on using the script that we've hacked up earlier: `dump_sym.sh`.
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
As expected, `main.statictmp_0` is a 8-byte variable whose value is `0x000000000000002a`, i.e. `$42`.

**Interface trick 3: Zero-values**

For this final trick, consider the following benchmark that instanciates an `eface<uint32>` from a zero-value ([eface_scalar_test.go](./eface_scalar_test.go)):
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

First, notice how we make use of `uint32(i - i)` instead of `uint32(0)` to prevent the compiler from falling back to optimization #2 (static inference).  
(*Sure, we could just have declared a global zero variable and the compiler would had been forced to take the conservative route too.. but then again, we're trying to have some fun here. Don't be that guy.*)  
The generated code now looks exactly like the normal, allocating case.. and still, it doesn't allocate. What's going on?

As we've mentioned earlier back when we were dissecting `runtime.convT2E32`, the allocation here can be optimized out using a trick similar to #1 (byte-sized values): when some code needs to reference a variable that holds a zero-value, the compiler simply gives it the address of a global variable exposed by the runtime whose value is always zero.  
Similarly to `runtime.staticbytes`, we can find this variable in the runtime code ([src/runtime/hashmap.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/hashmap.go#L1248-L1249)):
```Go
const maxZero = 1024 // must match value in ../cmd/compile/internal/gc/walk.go
var zeroVal [maxZero]byte
```

This ends our little tour of optimizations.  
We'll close this section with a summary of all the benchmarks that we've just looked at:
```Bash
$ go test -benchmem -bench=. ./eface_scalar_test.go
BenchmarkEfaceScalar/uint32-8         	2000000000	   0.54 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface32-8        	 100000000	   12.3 ns/op	  4 B/op     1 allocs/op
BenchmarkEfaceScalar/eface8-8         	2000000000	   0.88 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface-zeroval-8  	 500000000	   3.14 ns/op	  0 B/op     0 allocs/op
BenchmarkEfaceScalar/eface-static-8    	2000000000	   0.81 ns/op	  0 B/op     0 allocs/op
```

### A word about zero-values

As we've just seen, the `runtime.convT2*` family of functions avoids a heap allocation when the data to be held by the resulting interface happens to reference a zero-value.  
This optimization is not specific to interfaces and is actually part of a broader effort by the Go runtime to make sure that, when in need of a pointer to a zero-value, unnecessary allocations are avoided by taking the address of a special, always-zero variable exposed by the runtime.

We can confirm this with a simple program ([zeroval.go](./zeroval.go)):
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
As expected.

Note the `//go:linkname` directive which allows us to reference an external symbol:
> The //go:linkname directive instructs the compiler to use “importpath.name” as the object file symbol name for the variable or function declared as “localname” in the source code. Because this directive can subvert the type system and package modularity, it is only enabled in files that have imported "unsafe".

### A tangent about zero-size variables

In a similar vein as zero-values, a very common trick in Go programs is to rely on the fact that instanciating an object of size 0 (such as `struct{}{}`) doesn't result in an allocation.  
The official Go specification (linked at the end of this chapter) ends on a note that explains this:
> A struct or array type has size zero if it contains no fields (or elements, respectively) that have a size greater than zero.
> Two distinct zero-size variables may have the same address in memory.

The "may" in "may have the same address in memory" implies that the compiler doesn't guarantee this fact to be true, although it has always been and continues to be the case in the current implementation of the official Go compiler (`gc`).

As usual, we can confirm this with a simple program ([zerobase.go](./zerobase.go)):
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

If we'd like to know what hides behind this address, we can simply have a peek inside the binary:
```Bash
$ go build -o zerobase.bin zerobase.go && objdump -t zerobase.bin | grep 546fa8
0000000000546fa8 g     O .noptrbss	0000000000000008 runtime.zerobase
```
Then it's just a matter of finding this `runtime.zerobase` variable within the runtime source code ([src/runtime/malloc.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/malloc.go#L516-L517)):
```Go
// base address for all 0-byte allocations
var zerobase uintptr
```

And if we'd rather be really, really sure indeed:
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

There really is nothing special about interface composition, it merely is syntastic sugar exposed by the compiler.

Consider the following program ([compound_interface.go](./compound_interface.go)):
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

As usual, the compiler generates the corresponding `itab` for `iface<Mather, *Calculator>`:
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
We can see from the relocation directives that the virtual table generated by the compiler holds both the methods of `Adder` as well as those belonging to `Subber`, as we'd expect:
```
rel 24+8 t=1 "".(*Calculator).Add+0
rel 32+8 t=1 "".(*Calculator).Sub+0
```

Like we said, there's no secret sauce when it comes to interface composition.

On an unrelated note, this little program demonstrates something that we had not seen up until now: since the generated `itab` is specifically tailored to *a pointer to* a `Constructor`, as opposed to a concrete value, this fact gets reflected both in its symbol-name (`go.itab.*"".Calculator,"".Mather`) as well as in the `_type` that it embeds (`type.*"".Calculator`).  
This is consistent with the semantics used for naming method symbols, like we've seen earlier at the beginning of this chapter.

## Assertions

We'll close this chapter by looking at type assertions, both from an implementation and a cost standpoint.

### Type assertions

Consider this short program ([eface_to_type.go](./eface_to_type.go)):
```Go
var j uint32
var Eface interface{} // outsmart compiler (avoid static inference)

func assertion() {
    i := uint64(42)
    Eface = i
    j = Eface.(uint32)
}
```

Here's the annotated assembly listing for `j = Eface.(uint32)`:
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

Nothing surprising in there: the code compares the address held by `Eface._type` with the address of `type.uint32`, which, as we've seen before, is the global symbol exposed by the standard library that holds the content of the `_type` structure which describes an `uint32`.  
If the `_type` pointers match, then all is good and we're free to assign `*Eface.data` to `j`; otherwise, we call `runtime.panicdottypeE` to throw a panic message that precisely describes the mismatch.

`runtime.panicdottypeE` is a _very_ simple function that does no more than you'd expect ([src/runtime/iface.go](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/iface.go#L235-L245)):
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

Well, let's see what we've got here: a bunch of `MOV`s from main memory, a *very* predictable branch and, last but not least, a pointer dereference (`j = *Eface.data`) (which is only there because we've initialized our interface with a concrete value in the first place, otherwise we could just have copied the `Eface.data` pointer directly).

It's not even worth micro-benchmarking this, really.  
Similarly to the overhead of dynamic dispatch that we've measured earlier, this is in and of itself, in theory, almost free. How much it'll really cost you in practice will most likely be a matter of how your code-path is designed with regard to cache-friendliness & al.  
A simple micro-benchmark would probably be too skewed to tell us anything useful here, anyway.

All in all, we end up with the same old advice as usual: measure for your specific use case, check your processor's performance counters, and assert whether or not this has a visible impact on your hot path.  
It might. It might not. It most likely doesn't.

### Type-switches

Type-switches are a bit trickier, of course. Consider the following code ([eface_to_type.go](./eface_to_type.go)):
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

This quite simple type-switch statement translates into the following assembly (annotated):
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

Once again, if you meticulously step through the generated code and carefully read the corresponding annotations, you'll find that there's no dark magic in there.  
The control flow might look a bit convoluted at first, as it jumps back and forth a lot, but other than it's a pretty faithful rendition of the original Go code.

There are quite a few interesting things to note, though.

**Note 1: Layout**

First, notice the high-level layout of the generated code, which matches pretty closely the original switch statement:
1. We find an initial block of instructions that loads the `_type` of the variable we're interested in, and checks for `nil` pointers, just in case.
1. Then, we get N logical blocks that each correspond to one of the cases described in the original switch statement.
1. And finally, one last block defines a kind of indirect jump table that allows the control flow to jump from one case to the next while making sure to properly reset dirty registers on the way.

While obvious in hindsight, that second point is pretty important, as it implies that the number of instructions generated by a type-switch statement is purely a factor of the number of cases that it describes.  
In practice, this could lead to surprising performance issues as, for example, a massive type-switch statement with plenty of cases could generate a ton of instructions and end up thrashing the L1i cache if used on the wrong path.

Another interesting fact regarding the layout of our simple switch-statement above is the order in which the cases are set up in the generated code. In our original Go code, `case uint16` came first, followed by `case uint32`. In the assembly generated by the compiler, though, their orders have been reversed, with `case uint32` now being first and `case uint16` coming in second.  
That this reordering is a net win for us in this particular case is nothing but mere luck, AFAICT. In fact, if you take the time to experiment a bit with type-switches, especially ones with more than two cases, you'll find that the compiler always shuffles the cases using some kind of deterministic heuristics.  
What those heuristics are, I don't know (but as always, I'd love to if you do).

**Note 2: O(n)**

Second, notice how the control flow blindly jumps from one case to the next, until it either lands on one that evaluates to true or finally reaches the end of the switch statement.

Once again, while obvious when one actually stops to think about it ("how else could it work?"), this is easy to overlook when reasoning at a higher-level. In practice, this means that the cost of evaluating a type-switch statement grows linearly with its number of cases: it's `O(n)`.  
Likewise, evaluating a type-switch statement with N cases effectively has the same time-complexity as evaluating N type-assertions. As we've said, there's no magic here.

It's easy to confirm this with a bunch of benchmarks ([eface_to_type_test.go](./eface_to_type_test.go)):
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
With all its extra cases, the second type-switch does take almost twice as long per iteration indeed.

As an interesting exercise for the reader, try adding a `case uint32` in either one of the benchmarks above (anywhere), you'll see their performances improve drastically:
```Bash
benchstat <(go test -benchtime=1s -bench=. -count=3 ./eface_to_type_test.go)
name                        time/op
EfaceToType/switch-small-8  1.63ns ± 1%
EfaceToType/switch-big-8    2.17ns ± 1%
```
Using all the tools and knowledge that we've gathered during this chapter, you should be able to explain the rationale behind the numbers. Have fun!

**Note 3: Type hashes & pointer comparisons**

Finally, notice how the type comparisons in each cases are always done in two phases:
1. The types' hashes (`_type.hash`) are compared, and then
2. if they match, the respective memory-addresses of each `_type` pointers are compared directly.

Since each `_type` structure is generated once by the compiler and stored in a global variable in the `.rodata` section, we are guaranteed that each type gets assigned a unique address for the lifetime of the program.

In that context, it makes sense to do this extra pointer comparison in order to make sure that the successful match wasn't simply the result of a hash collision.. but then this raises an obvious question: why not just compare the pointers directly in the first place, and drop the notion of type hashes altogether? Especially when simple type assertions, as we've seen earlier, don't use type hashes at all.  
The answer is I don't have the slightest clue, and certainly would love some enlightment on this. As always, feel free to open an issue if you know more.

Speaking of type hashes, how is it that we know that `$-800397251` corresponds to `type.uint32.hash` and `$-269349216` to `type.uint16.hash`, you might wonder? The hard way, of course ([eface_type_hash.go](./eface_type_hash.go)):
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

That's it for interfaces.

I hope this chapter has given you most of the answers you were looking for when it comes to interfaces and their innards. Most importantly, it should have provided you with all the necessary tools and skills required to dig further whenever you'd need to.

If you have any questions or suggestions, don't hesitate to open an issue with the `chapter2:` prefix!

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
