<!-- Copyright © 2018 Clement Rey <cr.rey.clement@gmail.com>. -->
<!-- Licensed under the BY-NC-SA Creative Commons 4.0 International Public License. -->

# 第一章：  Go 汇编入门 

开发一些类似Go语言的抽象汇编语言，必须需要从研究运行时与标准库开始。本文会帮助你快速上手。

- *本章假设读者了解基本的汇编知识* 
- *当涉及系统架构时，总是假设为`linux/adm64`*
- *我们会默认开启编译器优化选项**enabled***

---

**内容目录**



---

*引号或注释中的内容属于来自官方文档，或者代码库，除非另有说明*

## "伪汇编"


Go 编译器输出一种抽象的，可移植的汇编代码，但这种汇编不会对应任何实际硬件。Go汇编器用伪汇编来生成目标机器的与硬件相关的指令代码。这种额外的抽象层有很多优势，最主要的是比较容易的将Go移植到新架构。更多的信息可以查看Rob Pike的*Go汇编器设计*,链接在本章最后。



> 了解Go的汇编器最重要的一点是，它不是一个底层机器的直接表现。一些是机器的细节映射，但一些不是。这是因为编译器不需要汇编器通过常用的管道。相反，编译器操作一种半抽象的指令集，不同架构指令的选择会在代码生成之后。汇编器工作在一种半抽象的形式，所以当你看到类似MOV的指令，工具链实际上生成了不仅mov这一条指令。可能有一个clear或者load。或者是机器指令相关的与mov对应的其他指令。通常特定机器的操作往往有自己的形式，而更通用的操作，类似内存移动、程序调用与返回，会更加抽象。细节随架构而变化，并且我们为不精确而抱歉；因为情况并不明确。
> 汇编程序去解析这种半抽象的指令集，将其转换为可以输入到连接器的指令。

## 分解一个简单程序

考虑下面的Go程序 ([direct_topfunc_call.go](./direct_topfunc_call.go)):
```Go
//go:noinline
func add(a, b int32) (int32, bool) { return a + b, true }

func main() { add(10, 32) }
```
*(Note the `//go:noinline` compiler-directive here... Don't get bitten.)*
*(注意`//go:noinline` 编译器指令... 别忽略)*

编译成汇编程序

```
$ GOOS=linux GOARCH=amd64 go tool compile -S direct_topfunc_call.go
```
```Assembly
0x0000 TEXT		"".add(SB), NOSPLIT, $0-16
  0x0000 FUNCDATA	$0, gclocals·f207267fbf96a0178e8758c6e3e0ce28(SB)
  0x0000 FUNCDATA	$1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x0000 MOVL		"".b+12(SP), AX
  0x0004 MOVL		"".a+8(SP), CX
  0x0008 ADDL		CX, AX
  0x000a MOVL		AX, "".~r2+16(SP)
  0x000e MOVB		$1, "".~r3+20(SP)
  0x0013 RET

0x0000 TEXT		"".main(SB), $24-0
  ;; ...omitted stack-split prologue...
  0x000f SUBQ		$24, SP
  0x0013 MOVQ		BP, 16(SP)
  0x0018 LEAQ		16(SP), BP
  0x001d FUNCDATA	$0, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x001d FUNCDATA	$1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
  0x001d MOVQ		$137438953482, AX
  0x0027 MOVQ		AX, (SP)
  0x002b PCDATA		$0, $0
  0x002b CALL		"".add(SB)
  0x0030 MOVQ		16(SP), BP
  0x0035 ADDQ		$24, SP
  0x0039 RET
  ;; ...omitted stack-split epilogue...
```
我们会一行行分析这两个函数，能更好地理解编译器做了什么

### 分析`add`

```Assembly
0x0000 TEXT "".add(SB), NOSPLIT, $0-16
```

- `0x0000`: 相对于函数开始位置，当前指令的偏移量 

- `TEXT "".add`: `TEXT` 指令声明`"".add`符号 是`.text`节(section)的一部分(i.e. 客运行代码) 暗示了接下来的指令是这个函数的主体.空字符串`""`在链接期间会被包名替代。例如, `"".add`链接为二进制后会变成`main.add` 

- `(SB)`: `SB` 是虚拟寄存器，保存了static-base指针，例如程序的起始地址。`"".add(SB)` 声明了我们的符号位于相对于内存空间起始地址的常量偏移处(通过链接器计算得到的)。它是全局符号表，是绝对的，直接地址。`objdump` 可以验证:
```
$ objdump -j .text -t direct_topfunc_call | grep 'main.add'
000000000044d980 g     F .text	000000000000000f main.add
```
> 所有用户定义的符号被写在伪寄存器FP(参数与局部的)和SB(全局的)保存的偏移量处。
> 伪寄存器SB被认为是内存起始，符号foo(SB)是foo名称的内存地址

- `NOSPLIT`: 意味着编译器*不*应该插入*stack-split*, 这个会检查当前栈是否需要增长。
  在`add`函数这个例子中，编译器自己设定了这个标志：它能知道`add`没有自己的局部变量和栈帧，当前栈不会增长，也就不需要CPU去检查栈是否需要增长。
> "NOSPLIT": 如果栈必须分裂，不要插入这个前导码。程序与它调用的子程序，必须满足栈的空间需求。栈分裂自己的代码会保护子程序。
> 我们会在本章结束时简单介绍一下协程(goroutines)和stack-splits。

- `$0-16`: `$0` 表示stack-frame的会被分配的空间大小，而`$16`特指传入参数的大小。

> 在通常情况下,帧的大小与参数的大小一致，通过一个减号分离开。帧大小$24-8表示函数有一个24字节大小的帧，它有8字节的参数存在于调用者的帧。如果在TEXT中没有指定NOSPLIT，必须要提供参数大小。在go语言中，汇编函数会用go vet 会检查参数是否正确。

```Assembly
0x0000 FUNCDATA $0, gclocals·f207267fbf96a0178e8758c6e3e0ce28(SB)
0x0000 FUNCDATA $1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
```

> FUNCDATA和PCDATA指令保存了垃圾回收器用到的相关信息，它们由编译器引入。

现在不需要担心，我们在介绍垃圾回收器时，会回来再解释。


```Assembly
0x0000 MOVL "".b+12(SP), AX
0x0004 MOVL "".a+8(SP), CX
```

Go的调用规则规定了每个参数必须通过栈转递，这个栈使用了调用者的stack-frame中预留的空间。调用者负责维护这个stack-frame的预留空间，确保被调用者的参数与返回值可以正确的传递。
Go编译器从不生成PUSH或者POP类的指令：栈的增长或减少通过增加或减小虚拟栈指针`SP`来实现。
> 伪寄存器SP是一个虚拟的栈指针，它被用来引用帧本地变量和调用者的传参。它指向本地栈帧顶部，所以偏移量应该为负数，在[-帧大小,0)区间内。例如：x-8(SP),y-4(SP)等。

尽管官方文档说“*所有用户定义的符号会作为偏移量被写到伪寄存器FP中*",这只会对手写代码才是正确的。像最近的编译器，Go工具生成的代码会直接从栈指针中使用参数和本地变量的偏移量。这样帧指针就可以被用做额外的通用寄存器，在哪些寄存器比较少的平台上，例如x86.可以参考本章最后的参考链接*stack frame layout on X86-64*，如果你喜欢这类的细节。

`"".b+12(SP)` 和 `"".a+8(SP)` 分别引用到从栈顶向下偏移地址12字节和8字节的内容（记住栈是向下生长的）。
`.a` 和 `.b` 是所引用的位置的随意的别称；尽管*它们绝对没有语义上的含义* ，无论如何，当使用虚拟寄存器中的相对地址就必须要使用它们。

关于虚拟的帧指针（FP），官方文档中这样说：
> FP 伪寄存器是一个虚拟的帧指针，用来引用函数参数。编译器维护一个虚拟帧指针并且用它来引用存在栈中的参数。0(FP)是函数的第一个参数，8(FP)是第二个(在64位系统中)，等等。然而，用这种方式引用一个函数参数，需要在开始定义一个名字，比如firs_arg+0(FP)和second_arg+8(FP)。（偏移量是指从帧指针开始的偏移量。这与SB不一样，SB保存的是从符号计算的偏移量）。汇编器会执行这种约定，拒绝纯粹的0(FP)和8（FP)。实际的名字是语义无关，但是应该记录参数的名称。

最后，两个事情需要重点提议下：
1. 第一个参数`a`不是位于`0(SP)`,而是在`8(SP)`；因为调用者会通过`CALL`为指令，使用`0(SP)`作为函数的返回值地址。

2. 参数的传递是反序的，第一个参数是最接近栈顶的。

```Assembly
0x0008 ADDL CX, AX
0x000a MOVL AX, "".~r2+16(SP)
0x000e MOVB $1, "".~r3+20(SP)
```

`ADDL`会将两个存储在`AX`和`CX`中的值相加，结果存储在`AX`中。L表示长字，例如4个字节。结果会移动到`"".~r2+16(SP)`，这是调用者事先预留存放返回值的地方。再一次，这里的`"".~r2`在语义上没有具体意义。

为了演示Go如何处理多个返回值，我们返回了一个boolen类型的常量`true`。这里的规则与我们第一个返回值是相同的，只有相对于`SP`的偏移量改变了。

```Assembly
0x0013 RET
```

最后`RET`伪指令告诉Go汇编器在这里需要从函数中返回，汇编器可以插入那些涉及到目标平台相关的返回指令。大部分情况会从`0(SP)`中弹出返回地址，并跳转到那里。
> 在TEXT这节中最后的指令必须是一些跳转指令，通常是RET指令。
> （如果不是这样，连接器会加入跳转到自己的指令；在TEXT中没有继续向下执行的情况。


这里一次性涉及了很多语法，我们先简单浏览一下刚才谈到的代码：

```Assembly
;; Declare global function symbol "".add (actually main.add once linked)
;; Do not insert stack-split preamble
;; 0 bytes of stack-frame, 16 bytes of arguments passed in
;; func add(a, b int32) (int32, bool)
0x0000 TEXT	"".add(SB), NOSPLIT, $0-16
  ;; ...omitted FUNCDATA stuff...
  0x0000 MOVL	"".b+12(SP), AX	    ;; move second Long-word (4B) argument from caller's stack-frame into AX
  0x0004 MOVL	"".a+8(SP), CX	    ;; move first Long-word (4B) argument from caller's stack-frame into CX
  0x0008 ADDL	CX, AX		    ;; compute AX=CX+AX
  0x000a MOVL	AX, "".~r2+16(SP)   ;; move addition result (AX) into caller's stack-frame
  0x000e MOVB	$1, "".~r3+20(SP)   ;; move `true` boolean (constant) into caller's stack-frame
  0x0013 RET			    ;; jump to return address stored at 0(SP)
```
总而言之，这里有个当被调用时的栈的结构图。
```
   |    +-------------------------+ <-- 32(SP)              
   |   |                         |                         
   |   |                         |                         
   |   |                         |                         
   |   | main.main's saved       |                         
   |   |     frame-pointer (BP)  |                         
   |   |-------------------------| <-- 24(SP)              
   |   |      [alignment]        |                         
向 |    | "".~r3 (bool) = 1/true  | <-- 21(SP)              
下 |    |-------------------------| <-- 20(SP)              
生 |    |                         |                         
长 |    | "".~r2 (int32) = 42     |                         
   |    |-------------------------| <-- 16(SP)              
   |    |                         |                         
   |    | "".b (int32) = 32       |                         
   |    |-------------------------| <-- 12(SP)              
   |    |                         |                         
   |    | "".a (int32) = 10       |                         
   |    |-------------------------| <-- 8(SP)               
   |    |                         |                         
   |    |                         |                         
   |    |                         |                         
 \ | /  | return address to       |                         
  \|/   |     main.main + 0x30    |                         
   -    +-------------------------+ <-- 0(SP) (TOP OF STACK)

(使用 https://textik.com 做图)
```
<!-- https://textik.com/#af55d3485eaa56f2 -->

### 分析`main`

我们为了方便你查看代码，在下面列举了`main`函数的代码片段。

```Assembly
0x0000 TEXT		"".main(SB), $24-0
  ;; ...omitted stack-split prologue...
  0x000f SUBQ		$24, SP
  0x0013 MOVQ		BP, 16(SP)
  0x0018 LEAQ		16(SP), BP
  ;; ...omitted FUNCDATA stuff...
  0x001d MOVQ		$137438953482, AX
  0x0027 MOVQ		AX, (SP)
  ;; ...omitted PCDATA stuff...
  0x002b CALL		"".add(SB)
  0x0030 MOVQ		16(SP), BP
  0x0035 ADDQ		$24, SP
  0x0039 RET
  ;; ...omitted stack-split epilogue...
```

```Assembly
0x0000 TEXT "".main(SB), $24-0
```

这里没有新的内容：

- `"".main`(链接后为`main.main`)是在`.text`节中的全局函数符号，其地址是从起始地址开始的某个常数偏移地址。
- main函数分配了24字节的栈帧，没有任何参数与返回值。

```Assembly
0x000f SUBQ     $24, SP
0x0013 MOVQ     BP, 16(SP)
0x0018 LEAQ     16(SP), BP
```

如我们之前提到的，Go的调用规则规定每个参数必须通过栈来传递。

我们的调用者通过减少虚拟栈指针的值来增长栈帧的空间（*记住栈的增长是向下的，所以`SUBQ`会让栈帧增长*）。
显然是24个字节：
- 8字节（`16(SP)`-`24(SP)`）是被用来存储当前的frame-poiter：`BP`，以便允许栈展开方便调试。
- 1+3 字节(`12(SP)`-`16(SP)`)是保留给第二个返回值(`bool`)以及在`amd64`架构上的三个对齐字节。
- 4个字节(`8(SP)`-`12(SP)`)是留给第一个返回值(`int32`)
- 4个字节 (`4(SP)`-`8(SP)`)是留给参数`b(int32)`
- 4个字节(`0(SP)`-`4(SP)`)是六个参数`a(int32)`

最后，随着栈的增长，`LEAQ`计算frame-pointer的新的地址，并存储在`BP`里。


```Assembly
0x001d MOVQ     $137438953482, AX
0x0027 MOVQ     AX, (SP)
```

调用者将参数压入刚增长的栈的最顶端，通过带有**Q**结尾的指令可以操作8字节的数据。
尽管这个数字看起来像是随机数，`137438953482` 实际上代表了`10`与`23`，只是将`10`和`32`这两个数字的4字节二进制表示形式按照十进制数字来显示了。如下图：

```
$ echo 'obase=2;137438953482' | bc
10000000000000000000000000000000001010
\_____/\_____________________________/
   32                             10
```

```Assembly
0x002b CALL     "".add(SB)
```

我们`CALL`(调用)`add`函数，使用了一个相对与static-base指针(全局符号寄存器)的偏移量。这实际上直接跳转到add函数地址。
注意`CALL`也会将返回值(8字节)压入栈顶，所以每次引用`SP`，需要在`add`函数最后偏移8个字节！例如 `"".a`不是`0(SP)`而是 `8(SP)`。

```Assembly
0x0030 MOVQ     16(SP), BP
0x0035 ADDQ     $24, SP
0x0039 RET
```

最后：
1. 通过栈来恢复最初的frame-pointer的值（保存在BP中）。
2. 收缩我们之前分配的24个字节的栈的空间。
3. 要求Go汇编器插入返回相关的指令

## 关于goroutines，stacks和splits

现在不适合介绍goroutine的细节(稍后会提出),但是由于已经开始研究汇编，我们会越来越熟悉涉及栈管理的指令代码。我们应该快速的了解一下这些部分，了解常见的思想以及这样设计的原因。


### Stacks（栈）

由于Go语言程序的协程数量是不确定的，并且在实际中可能达到数百万，运行时(runtime)必须在给每个协程分配栈空间时采取保守策略，以免吃光可用内存。
因此，每个新的协程创建时会分配到2k左右的栈空间(据说栈实际上会被分配到堆上)。
随着一个协程执行，可能会需要更多的内存。为了防止栈溢出，运行时(runtime)需要在溢出发生之前，再为协程分配一个比之前两倍大的空间，原来栈中的内容会被拷贝到新的栈中。
这个过程被称为*stack-split*，并且可以提供有效的动态栈。

### Splits（拆分)

对于栈拆分的工作，编译器在每个函数的开始与结尾插入少量指令，以保护栈不会溢出。在本章开始时我们看到，为了防止额外的开销，不需要增长栈空间的函数会使用`NOSPLIT`告诉编译器不要插入这些检查指令。

让我们看一下之前的main函数，这次没有省略stack-split前导码：

```Assembly
0x0000 TEXT	"".main(SB), $24-0
  ;; stack-split prologue
  0x0000 MOVQ	(TLS), CX
  0x0009 CMPQ	SP, 16(CX)
  0x000d JLS	58

  0x000f SUBQ	$24, SP
  0x0013 MOVQ	BP, 16(SP)
  0x0018 LEAQ	16(SP), BP
  ;; ...omitted FUNCDATA stuff...
  0x001d MOVQ	$137438953482, AX
  0x0027 MOVQ	AX, (SP)
  ;; ...omitted PCDATA stuff...
  0x002b CALL	"".add(SB)
  0x0030 MOVQ	16(SP), BP
  0x0035 ADDQ	$24, SP
  0x0039 RET

  ;; stack-split epilogue
  0x003a NOP
  ;; ...omitted PCDATA stuff...
  0x003a CALL	runtime.morestack_noctxt(SB)
  0x003f JMP	0
```

可以看到，stack-split前导码被分成两部分：序言与结语
- 序言检查协程是否马上要越界，如果要越界了，就跳转到结语
- 另一方面，结语会触发栈增长，之后跳转回序言。

这创建了一个反馈式的循环，会持续为需要内存的协程分配内存。

**序幕**

```Assembly
0x0000 MOVQ	(TLS), CX   ;; store current *g in CX
0x0009 CMPQ	SP, 16(CX)  ;; compare SP and g.stackguard0
0x000d JLS	58	    ;; jumps to 0x3a if SP <= g.stackguard0
```
`TLS`是运行时的虚拟寄存器，保存了当前`g`的指针。其数据结构可以跟踪一个协程的所有状态。

让我们开下运行时中`g`的源码。

```Go
type g struct {
	stack       stack   // 16 bytes
	// stackguard0 is the stack pointer compared in the Go stack growth prologue.
	// It is stack.lo+StackGuard normally, but can be StackPreempt to trigger a preemption.
	stackguard0 uintptr
	stackguard1 uintptr

	// ...omitted dozens of fields...
}
```

我们可以看到`16(CX)`对应于`g.stackguard0`，这是运行时维护的一个阈值，与stack-pointer相比较，可以指示一个协程是否会超出内存。
序言会检查当前的 `SP`值是否小于或等于`stackguard0`阈值，如果超出则会跳转到结语处。


**结语**
```Assembly
0x003a NOP
0x003a CALL	runtime.morestack_noctxt(SB)
0x003f JMP	0
```
结语很直接：调用运行时的增长栈空间的函数，之后会返回到函数开始处(序言那里)

The `NOP` instruction just before the `CALL` exists so that the prologue doesn't jump directly onto a `CALL` instruction. On some platforms, doing so can lead to very dark places; it's common pratice to set-up a noop instruction right before the actual call and land on this `NOP` instead.

在`CALL`指令之前有一个`NOP`，这样结语不会直接跳转`CALL`指令，在有些平台上这样做是非常不好；通常的经验是在实际的call之前设置一个空操作(nop)，而不是设置一个`NOP`。

### 没有提及一些技巧
我们仅在这里介绍冰山一角。
栈增长的内部机制我们有很多没有提及的技巧。整个过程是十分复杂的机制，需要单独的一章来介绍。我们会及时做介绍的。

## 总结
Go汇编的简单介绍应该能让你上手，随着我们在本书中深入解析Go的内幕，Go 汇编会成为我们了解这些内幕的最依赖的工具之一，串联那些看起来没什么关系的要点。如果你有问题或建议，尽管开一个issue提问吧。


## 参考链接

- [[Official] A Quick Guide to Go's Assembler](https://golang.org/doc/asm)
- [[Official] Go Compiler Directives](https://golang.org/cmd/compile/#hdr-Compiler_Directives)
- [[Official] The design of the Go Assembler](https://www.youtube.com/watch?v=KINIAgRpkDA)
- [[Official] Contiguous stacks Design Document](https://docs.google.com/document/d/1wAaf1rYoM4S4gtnPh0zOlGzWtrZFQ5suE8qr2sD8uWQ/pub)
- [[Official] The `_StackMin` constant](https://github.com/golang/go/blob/ea8d7a370d66550d587414cc0cab650f35400f94/src/runtime/stack.go#L70-L71)
- [A Foray Into Go Assembly Programming](https://blog.sgmansfield.com/2017/04/a-foray-into-go-assembly-programming/)
- [Dropping Down Go Functions in Assembly](https://www.youtube.com/watch?v=9jpnFmJr2PE)
- [What is the purpose of the EBP frame pointer register?](https://stackoverflow.com/questions/579262/what-is-the-purpose-of-the-ebp-frame-pointer-register)
- [Stack frame layout on x86-64](https://eli.thegreenplace.net/2011/09/06/stack-frame-layout-on-x86-64)
- [How Stacks are Handled in Go](https://blog.cloudflare.com/how-stacks-are-handled-in-go/)
- [Why stack grows down](https://gist.github.com/cpq/8598782)
