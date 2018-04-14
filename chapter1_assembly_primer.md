# README

```bash
$ go version
go version go1.10 linux/amd64
```

## Chapter I: A Primer on Go Assembly

Developing some familiarity with Go's abstract assembly language is a must before we can start delving into the implementation of the runtime & standard library.  
This quick guide should hopefully get you up-to-speed.

**Table of Contents**  

* ["Pseudo-assembly"](chapter1_assembly_primer.md#pseudo-assembly)
* [Decomposing a simple program](chapter1_assembly_primer.md#decomposing-a-simple-program)
  * [Dissecting `add`](chapter1_assembly_primer.md#dissecting-add)
  * [Dissecting `main`](chapter1_assembly_primer.md#dissecting-main)
* [A word about goroutines, stacks and splits](chapter1_assembly_primer.md#a-word-about-goroutines-stacks-and-splits)
  * [Stacks](chapter1_assembly_primer.md#stacks)
  * [Splits](chapter1_assembly_primer.md#splits)
  * [Minus some subtleties](chapter1_assembly_primer.md#minus-some-subtleties)
* [Conclusion](chapter1_assembly_primer.md#conclusion)
* [Links](chapter1_assembly_primer.md#links)
* _This chapter assumes some basic knowledge of any kind of assembler._
* _If and when running into architecture-specific matters, always assume _`linux/amd64`_._
* _We will always work with compiler optimizations **enabled**._
* _Quoted text and/or comments always come from the official documentation and/or codebase, unless stated otherwise._

### "Pseudo-assembly"

The Go compiler outputs an abstract, portable form of assembly that doesn't actually map to any real hardware. The Go assembler then uses this pseudo-assembly output in order to generate concrete, machine-specific instructions for the targeted hardware.  
This extra layer has many benefits, the main one being how easy it makes porting Go to new architectures. For more information, have a look at Rob Pike's _The Design of the Go Assembler_, listed in the links at the end of this chapter.

> The most important thing to know about Go's assembler is that it is not a direct representation of the underlying machine. Some of the details map precisely to the machine, but some do not. This is because the compiler suite needs no assembler pass in the usual pipeline. Instead, the compiler operates on a kind of semi-abstract instruction set, and instruction selection occurs partly after code generation. The assembler works on the semi-abstract form, so when you see an instruction like MOV what the toolchain actually generates for that operation might not be a move instruction at all, perhaps a clear or load. Or it might correspond exactly to the machine instruction with that name. In general, machine-specific operations tend to appear as themselves, while more general concepts like memory move and subroutine call and return are more abstract. The details vary with architecture, and we apologize for the imprecision; the situation is not well-defined.
>
> The assembler program is a way to parse a description of that semi-abstract instruction set and turn it into instructions to be input to the linker.

### Decomposing a simple program

Consider the following Go code \([direct\_topfunc\_call.go](https://github.com/teh-cmc/go-internals/tree/27fcccf8e8ba6a009989ac199f57d2502e867b8c/chapter1_assembly_primer/direct_topfunc_call.go)\):

```go
//go:noinline
func add(a, b int32) (int32, bool) { return a + b, true }

func main() { add(10, 32) }
```

_\(Note the _`//go:noinline`_ compiler-directive here... Don't get bitten.\)_

Let's compile this down to assembly:

```text
$ GOOS=linux GOARCH=amd64 go tool compile -S direct_topfunc_call.go
```

```text
0x0000 TEXT        "".add(SB), NOSPLIT, $0-16
  0x0000 FUNCDATA    $0, gclocals·f207267fbf96a0178e8758c6e3e0ce28(SB)
  0x0000 FUNCDATA    $1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x0000 MOVL        "".b+12(SP), AX
  0x0004 MOVL        "".a+8(SP), CX
  0x0008 ADDL        CX, AX
  0x000a MOVL        AX, "".~r2+16(SP)
  0x000e MOVB        $1, "".~r3+20(SP)
  0x0013 RET

0x0000 TEXT        "".main(SB), $24-0
  ;; ...omitted stack-split prologue...
  0x000f SUBQ        $24, SP
  0x0013 MOVQ        BP, 16(SP)
  0x0018 LEAQ        16(SP), BP
  0x001d FUNCDATA    $0, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x001d FUNCDATA    $1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x001d MOVQ        $137438953482, AX
  0x0027 MOVQ        AX, (SP)
  0x002b PCDATA        $0, $0
  0x002b CALL        "".add(SB)
  0x0030 MOVQ        16(SP), BP
  0x0035 ADDQ        $24, SP
  0x0039 RET
  ;; ...omitted stack-split epilogue...
```

We'll dissect those 2 functions line-by-line in order to get a better understanding of what the compiler is doing.

#### Dissecting `add`

```text
0x0000 TEXT "".add(SB), NOSPLIT, $0-16
```

* `0x0000`: Offset of the current instruction, relative to the start of the function.
* `TEXT "".add`: The `TEXT` directive declares the `"".add` symbol as part of the `.text` section \(i.e. runnable code\) and indicates that the instructions that follow are the body of the function. The empty string `""` will be replaced by the name of the current package at link-time: i.e., `"".add` will become `main.add` once linked into our final binary.
* `(SB)`: `SB` is the virtual register that holds the "static-base" pointer, i.e. the address of the beginning of the address-space of our program.  
  `"".add(SB)` declares that our symbol is located at some constant offset \(computed by the linker\) from the start of our address-space. Put differently, it has an absolute, direct address: it's a global function symbol.  
  Good ol' `objdump` will confirm all of that for us:

  ```text
  $ objdump -j .text -t direct_topfunc_call | grep 'main.add'
  000000000044d980 g     F .text    000000000000000f main.add
  ```

  > All user-defined symbols are written as offsets to the pseudo-registers FP \(arguments and locals\) and SB \(globals\).  
  > The SB pseudo-register can be thought of as the origin of memory, so the symbol foo\(SB\) is the name foo as an address in memory.

* `NOSPLIT`: Indicates to the compiler that it should _not_ insert the _stack-split_ preamble, which checks whether the current stack needs to be grown.  
  In the case of our `add` function, the compiler has set the flag by itself: it is smart enough to figure that, since `add` has no local variables and no stack-frame of its own, it simply cannot outgrow the current stack; thus it'd be a complete waste of CPU cycles to run these checks at each call site.

  > "NOSPLIT": Don't insert the preamble to check if the stack must be split. The frame for the routine, plus anything it calls, must fit in the spare space at the top of the stack segment. Used to protect routines such as the stack splitting code itself.  
  > We'll have a quick word about goroutines and stack-splits at the end this chapter.

* `$0-16`: `$0` denotes the size in bytes of the stack-frame that will be allocated; while `$16` specifies the size of the arguments passed in by the caller.

  > In the general case, the frame size is followed by an argument size, separated by a minus sign. \(It's not a subtraction, just idiosyncratic syntax.\) The frame size $24-8 states that the function has a 24-byte frame and is called with 8 bytes of argument, which live on the caller's frame. If NOSPLIT is not specified for the TEXT, the argument size must be provided. For assembly functions with Go prototypes, go vet will check that the argument size is correct.

```text
0x0000 FUNCDATA $0, gclocals·f207267fbf96a0178e8758c6e3e0ce28(SB)
0x0000 FUNCDATA $1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
```

> The FUNCDATA and PCDATA directives contain information for use by the garbage collector; they are introduced by the compiler.

Don't worry about this for now; we'll come back to it when diving into garbage collection later in the book.

```text
0x0000 MOVL "".b+12(SP), AX
0x0004 MOVL "".a+8(SP), CX
```

The Go calling convention mandates that every argument must be passed on the stack, using the pre-reserved space on the caller's stack-frame.  
It is the caller's responsibility to grow \(and shrink back\) the stack appropriately so that arguments can be passed to the callee, and potential return-values passed back to the caller.

The Go compiler never generates instructions from the PUSH/POP family: the stack is grown or shrunk by respectively decrementing or incrementing the virtual stack pointer `SP`.

> The SP pseudo-register is a virtual stack pointer used to refer to frame-local variables and the arguments being prepared for function calls. It points to the top of the local stack frame, so references should use negative offsets in the range \[−framesize, 0\): x-8\(SP\), y-4\(SP\), and so on.

Although the official documentation states that "_All user-defined symbols are written as offsets to the pseudo-register FP \(arguments and locals\)_", this is only ever true for hand-written code.  
Like most recent compilers, the Go tool suite always references argument and locals using offsets from the stack-pointer directly in the code it generates. This allows for the frame-pointer to be used as an extra general-purpose register on platform with fewer registers \(e.g. x86\).  
Have a look at _Stack frame layout on x86-64_ in the links at the end of this chapter if you enjoy this kind of nitty gritty details.  
_\[UPDATE: We've discussed about this matter in _[_issue \#2: Frame pointer_](https://github.com/teh-cmc/go-internals/issues/2)_.\]_

`"".b+12(SP)` and `"".a+8(SP)` respectively refer to the addresses 12 bytes and 8 bytes below the top of the stack \(remember: it grows downwards!\).  
`.a` and `.b` are arbitrary aliases given to the referred locations; although _they have absolutely no semantic meaning_ whatsoever, they are mandatory when using relative addressing on virtual registers. The documentation about the virtual frame-pointer has some to say about this:

> The FP pseudo-register is a virtual frame pointer used to refer to function arguments. The compilers maintain a virtual frame pointer and refer to the arguments on the stack as offsets from that pseudo-register. Thus 0\(FP\) is the first argument to the function, 8\(FP\) is the second \(on a 64-bit machine\), and so on. However, when referring to a function argument this way, it is necessary to place a name at the beginning, as in first\_arg+0\(FP\) and second\_arg+8\(FP\). \(The meaning of the offset —offset from the frame pointer— distinct from its use with SB, where it is an offset from the symbol.\) The assembler enforces this convention, rejecting plain 0\(FP\) and 8\(FP\). The actual name is semantically irrelevant but should be used to document the argument's name.

Finally, there are two important things to note here: 1. The first argument `a` is not located at `0(SP)`, but rather at `8(SP)`; that's because the caller stores its return-address in `0(SP)` via the `CALL` pseudo-instruction. 2. Arguments are passed in reverse-order; i.e. the first argument is the closest to the top of the stack.

```text
0x0008 ADDL CX, AX
0x000a MOVL AX, "".~r2+16(SP)
0x000e MOVB $1, "".~r3+20(SP)
```

`ADDL` does the actual addition of the two **L**ong-words \(i.e. 4-byte values\) stored in `AX` and `CX`, then stores the final result in `AX`.  
That result is then moved over to `"".~r2+16(SP)`, where the caller had previously reserved some stack space and expects to find its return values. Once again, `"".~r2` has no semantic meaning here.

To demonstrate how Go handles multiple return-values, we're also returning a constant `true` boolean value.  
The mechanics at play are exactly the same as for our first return value; only the offset relative to `SP` changes.

```text
0x0013 RET
```

A final `RET` pseudo-instruction tells the Go assembler to insert whatever instructions are required by the calling convention of the target platform in order to properly return from a subroutine call.  
Most likely this will cause the code to pop off the return-address stored at `0(SP)` then jump back to it.

> The last instruction in a TEXT block must be some sort of jump, usually a RET \(pseudo-\)instruction. \(If it's not, the linker will append a jump-to-itself instruction; there is no fallthrough in TEXTs.\)

That's a lot of syntax and semantics to ingest all at once. Here's a quick inlined summary of what we've just covered:

```text
;; Declare global function symbol "".add (actually main.add once linked)
;; Do not insert stack-split preamble
;; 0 bytes of stack-frame, 16 bytes of arguments passed in
;; func add(a, b int32) (int32, bool)
0x0000 TEXT    "".add(SB), NOSPLIT, $0-16
  ;; ...omitted FUNCDATA stuff...
  0x0000 MOVL    "".b+12(SP), AX        ;; move second Long-word (4B) argument from caller's stack-frame into AX
  0x0004 MOVL    "".a+8(SP), CX        ;; move first Long-word (4B) argument from caller's stack-frame into CX
  0x0008 ADDL    CX, AX            ;; compute AX=CX+AX
  0x000a MOVL    AX, "".~r2+16(SP)   ;; move addition result (AX) into caller's stack-frame
  0x000e MOVB    $1, "".~r3+20(SP)   ;; move `true` boolean (constant) into caller's stack-frame
  0x0013 RET                ;; jump to return address stored at 0(SP)
```

All in all, here's a visual representation of what the stack looks like when `main.add` has finished executing:

```text
   |    +-------------------------+ <-- 32(SP)              
   |    |                         |                         
 G |    |                         |                         
 R |    |                         |                         
 O |    | main.main's saved       |                         
 W |    |     frame-pointer (BP)  |                         
 S |    |-------------------------| <-- 24(SP)              
   |    |      [alignment]        |                         
 D |    | "".~r3 (bool) = 1/true  | <-- 21(SP)              
 O |    |-------------------------| <-- 20(SP)              
 W |    |                         |                         
 N |    | "".~r2 (int32) = 42     |                         
 W |    |-------------------------| <-- 16(SP)              
 A |    |                         |                         
 R |    | "".b (int32) = 32       |                         
 D |    |-------------------------| <-- 12(SP)              
 S |    |                         |                         
   |    | "".a (int32) = 10       |                         
   |    |-------------------------| <-- 8(SP)               
   |    |                         |                         
   |    |                         |                         
   |    |                         |                         
 \ | /  | return address to       |                         
  \|/   |     main.main + 0x30    |                         
   -    +-------------------------+ <-- 0(SP) (TOP OF STACK)

(diagram made with https://textik.com)
```

#### Dissecting `main`

We'll spare you some unnecessary scrolling, here's a reminder of what our `main` function looks like:

```text
0x0000 TEXT        "".main(SB), $24-0
  ;; ...omitted stack-split prologue...
  0x000f SUBQ        $24, SP
  0x0013 MOVQ        BP, 16(SP)
  0x0018 LEAQ        16(SP), BP
  ;; ...omitted FUNCDATA stuff...
  0x001d MOVQ        $137438953482, AX
  0x0027 MOVQ        AX, (SP)
  ;; ...omitted PCDATA stuff...
  0x002b CALL        "".add(SB)
  0x0030 MOVQ        16(SP), BP
  0x0035 ADDQ        $24, SP
  0x0039 RET
  ;; ...omitted stack-split epilogue...
```

```text
0x0000 TEXT "".main(SB), $24-0
```

Nothing new here:

* `"".main` \(`main.main` once linked\) is a global function symbol in the `.text` section, whose address is some constant offset from the beginning of our address-space.
* It allocates a 24 bytes stack-frame and doesn't receive any argument nor does it return any value.

```text
0x000f SUBQ     $24, SP
0x0013 MOVQ     BP, 16(SP)
0x0018 LEAQ     16(SP), BP
```

As we mentioned above, the Go calling convention mandates that every argument must be passed on the stack.

Our caller, `main`, grows its stack-frame by 24 bytes \(_remember that the stack grows downwards, so _`SUBQ`_ here actually makes the stack-frame bigger_\) by decrementing the virtual stack-pointer. Of those 24 bytes:

* 8 bytes \(`16(SP)`-`24(SP)`\) are used to store the current value of the frame-pointer `BP` \(_the real one!_\) to allow for stack-unwinding and facilitate debugging
* 1+3 bytes \(`12(SP)`-`16(SP)`\) are reserved for the second return value \(`bool`\) plus 3 bytes of necessary alignment on `amd64`
* 4 bytes \(`8(SP)`-`12(SP)`\) are reserved for the first return value \(`int32`\)
* 4 bytes \(`4(SP)`-`8(SP)`\) are reserved for the value of argument `b (int32)`
* 4 bytes \(`0(SP)`-`4(SP)`\) are reserved for the value of argument `a (int32)`

Finally, following the growth of the stack, `LEAQ` computes the new address of the frame-pointer and stores it in `BP`.

```text
0x001d MOVQ     $137438953482, AX
0x0027 MOVQ     AX, (SP)
```

The caller pushes the arguments for the callee as a **Q**uad word \(i.e. an 8-byte value\) at the top of the stack that it has just grown.  
Although it might look like random garbage at first, `137438953482` actually corresponds to the `10` and `32` 4-byte values concatenated into one 8-byte value:

```text
$ echo 'obase=2;137438953482' | bc
10000000000000000000000000000000001010
\_____/\_____________________________/
   32                             10
```

```text
0x002b CALL     "".add(SB)
```

We `CALL` our `add` function as an offset relative to the static-base pointer: i.e. this is a straightforward jump to a direct address.

Note that `CALL` also pushes the return-address \(8-byte value\) at the top of the stack; so every references to `SP` made from within our `add` function end up being offsetted by 8 bytes!  
E.g. `"".a` is not at `0(SP)` anymore, but at `8(SP)`.

```text
0x0030 MOVQ     16(SP), BP
0x0035 ADDQ     $24, SP
0x0039 RET
```

Finally, we: 1. Unwind the frame-pointer by one stack-frame \(i.e. we "go down" one level\) 2. Shrink the stack by 24 bytes to reclaim the stack space we had previously allocated 3. Ask the Go assembler to insert subroutine-return related stuff

### A word about goroutines, stacks and splits

Now is not the time nor place to delve into goroutines' internals \(_..that comes later_\), but as we start looking at assembly dumps more and more, instructions related to stack management will rapidly become a very familiar sight.  
We should be able to quickly recognize these patterns, and, while we're at it, understand the general idea of what they do and why do they do it.

#### Stacks

Since the number of goroutines in a Go program is non-deterministic, and can go up to several millions in practice, the runtime must take the conservative route when allocating stack space for goroutines to avoid eating up all of the available memory.  
As such, every new goroutine is given an initial tiny 2kB stack by the runtime \(said stack is actually allocated on the heap behind the scenes\).

As a goroutine runs along doing its job, it might end up outgrowing its contrived, initial stack-space \(i.e. stack-overflow\).  
To prevent this from happening, the runtime makes sure that when a goroutine is running out of stack, a new, bigger stack with two times the size of the old one gets allocated, and that the content of the original stack gets copied over to the new one.  
This process is known as a _stack-split_ and effectively makes goroutine stacks dynamically-sized.

#### Splits

For stack-splitting to work, the compiler inserts a few instructions at the beginning and end of every function that could potentially overflow its stack.  
As we've seen earlier in this chapter, and to avoid unnecessary overhead, functions that cannot possibly outgrow their stack are marked as `NOSPLIT` as a hint for the compiler not to insert these checks.

Let's look at our main function from earlier, this time without omitting the stack-split preamble:

```text
0x0000 TEXT    "".main(SB), $24-0
  ;; stack-split prologue
  0x0000 MOVQ    (TLS), CX
  0x0009 CMPQ    SP, 16(CX)
  0x000d JLS    58

  0x000f SUBQ    $24, SP
  0x0013 MOVQ    BP, 16(SP)
  0x0018 LEAQ    16(SP), BP
  ;; ...omitted FUNCDATA stuff...
  0x001d MOVQ    $137438953482, AX
  0x0027 MOVQ    AX, (SP)
  ;; ...omitted PCDATA stuff...
  0x002b CALL    "".add(SB)
  0x0030 MOVQ    16(SP), BP
  0x0035 ADDQ    $24, SP
  0x0039 RET

  ;; stack-split epilogue
  0x003a NOP
  ;; ...omitted PCDATA stuff...
  0x003a CALL    runtime.morestack_noctxt(SB)
  0x003f JMP    0
```

As you can see, the stack-split preamble is divided into a prologue and an epilogue:

* The prologue checks whether the goroutine is running out of space and, if it's the case, jumps to the epilogue.
* The epilogue, on the other hand, triggers the stack-growth machinery and then jumps back to the prologue.

This creates a feedback loop that goes on for as long as a large enough stack hasn't been allocated for our starved goroutine.

**Prologue**

```text
0x0000 MOVQ    (TLS), CX   ;; store current *g in CX
0x0009 CMPQ    SP, 16(CX)  ;; compare SP and g.stackguard0
0x000d JLS    58        ;; jumps to 0x3a if SP <= g.stackguard0
```

`TLS` is a virtual register maintained by the runtime that holds a pointer to the current `g`, i.e. the data-structure that keeps track of all the state of a goroutine.

Looking at the definition of `g` from the source code of the runtime:

```go
type g struct {
    stack       stack   // 16 bytes
    // stackguard0 is the stack pointer compared in the Go stack growth prologue.
    // It is stack.lo+StackGuard normally, but can be StackPreempt to trigger a preemption.
    stackguard0 uintptr
    stackguard1 uintptr

    // ...omitted dozens of fields...
}
```

We can see that `16(CX)` corresponds to `g.stackguard0`, which is the threshold value maintained by the runtime that, when compared to the stack-pointer, indicates whether or not a goroutine is about to run out of space.  
The prologue thus checks if the current `SP` value is less than or equal to the `stackguard0` threshold \(that is, it's bigger\), then jumps to the epilogue if it happens to be the case.

**Epilogue**

```text
0x003a NOP
0x003a CALL    runtime.morestack_noctxt(SB)
0x003f JMP    0
```

The body of the epilogue is pretty straightforward: it calls into the runtime, which will do the actual work of growing the stack, then jumps back to the first instruction of the function \(i.e. to the prologue\).

The `NOP` instruction just before the `CALL` exists so that the prologue doesn't jump directly onto a `CALL` instruction. On some platforms, doing so can lead to very dark places; it's a common pratice to set-up a noop instruction right before the actual call and land on this `NOP` instead.  
_\[UPDATE: We've discussed about this matter in _[_issue \#4: Clarify "nop before call" paragraph_](https://github.com/teh-cmc/go-internals/issues/4)_.\]_

#### Minus some subtleties

We've merely covered the tip of the iceberg here.  
The inner mechanics of stack-growth have many more subtleties that we haven't even mentioned here: the whole process is quite a complex machinery overall, and will require a chapter of its own.

We'll come back to these matters in time.

### Conclusion

This quick introduction to Go's assembler should give you enough material to start toying around.

As we dig deeper and deeper into Go's internals for the rest of this book, Go assembly will be one of our most relied-on tool to understand what goes on behind the scenes and connect the, at first sight, not-always-so-obvious dots.

If you have any questions or suggestions, don't hesitate to open an issue with the `chapter1:` prefix!

### Links

* [\[Official\] A Quick Guide to Go's Assembler](https://golang.org/doc/asm)
* [\[Official\] Go Compiler Directives](https://golang.org/cmd/compile/#hdr-Compiler_Directives)
* [\[Official\] The design of the Go Assembler](https://www.youtube.com/watch?v=KINIAgRpkDA)
* [\[Official\] Contiguous stacks Design Document](https://docs.google.com/document/d/1wAaf1rYoM4S4gtnPh0zOlGzWtrZFQ5suE8qr2sD8uWQ/pub)
* [\[Official\] The `_StackMin` constant](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/stack.go#L70-L71)
* [\[Discussion\] Issue \#2: _Frame pointer_](https://github.com/teh-cmc/go-internals/issues/2)
* [\[Discussion\] Issue \#4: _Clarify "nop before call" paragraph_](https://github.com/teh-cmc/go-internals/issues/4)
* [A Foray Into Go Assembly Programming](https://blog.sgmansfield.com/2017/04/a-foray-into-go-assembly-programming/)
* [Dropping Down Go Functions in Assembly](https://www.youtube.com/watch?v=9jpnFmJr2PE)
* [What is the purpose of the EBP frame pointer register?](https://stackoverflow.com/questions/579262/what-is-the-purpose-of-the-ebp-frame-pointer-register)
* [Stack frame layout on x86-64](https://eli.thegreenplace.net/2011/09/06/stack-frame-layout-on-x86-64)
* [How Stacks are Handled in Go](https://blog.cloudflare.com/how-stacks-are-handled-in-go/)
* [Why stack grows down](https://gist.github.com/cpq/8598782)

