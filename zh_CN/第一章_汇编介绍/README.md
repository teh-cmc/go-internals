<!-- Copyright © 2018 Clement Rey <cr.rey.clement@gmail.com>. -->
<!-- Licensed under the BY-NC-SA Creative Commons 4.0 International Public License. -->

```Bash
$ go version
go version go1.10 linux/amd64
```

# 第一章: Go 汇编介绍

在深入 Go 语言运行时和标准库实现前，对 Go 语言抽象汇编语言的熟悉了解是必要的前提条件。希望这章快速指导能够帮助你加速这个过程。

---

**目录**
<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->


- [伪汇编](#%E4%BC%AA%E6%B1%87%E7%BC%96)
- [分解一个简单的程序](#%E5%88%86%E8%A7%A3%E4%B8%80%E4%B8%AA%E7%AE%80%E5%8D%95%E7%9A%84%E7%A8%8B%E5%BA%8F)
  - [`add` 解析](#add-%E8%A7%A3%E6%9E%90)
  - [`main` 解析](#main-%E8%A7%A3%E6%9E%90)
- [goroutines, stacks and splits 介绍](#goroutines-stacks-and-splits-%E4%BB%8B%E7%BB%8D)
  - [Stacks](#stacks)
  - [Splits](#splits)
  - [省略的细节](#%E7%9C%81%E7%95%A5%E7%9A%84%E7%BB%86%E8%8A%82)
- [总结](#%E6%80%BB%E7%BB%93)
- [链接](#%E9%93%BE%E6%8E%A5)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

---

- *本章需要你对汇编知识有一些了解*
- *当面对操作系统，CPU架构特定的场景时，我们假设你是 `linux/amd64`。*
- *我们所有的测试都是在编译器优化选项**启用**的情况下。*
- *除特殊声明外，所有被引用的文本或者注释都是摘至 Go 语言官方文档和代码。*

## 伪汇编

Go 编译器输出一种抽象的，可移植的汇编代码，这种汇编代码实际上不能直接映射到任何实际的硬件。Go 汇编器编译这种伪汇编代码，输出特定目标硬件的机器指令。
这额外的中间层有很多优势，最主要的优势是简化 Go 语言新架构移植。更多信息参考本章末的链接，*A Quick Guide to Go's Assembler*，作者 Rob Pike。

> 关于 Go 汇编器最重要的一点是，它不是对底层机器的直接表示。一些细节直接映射到机器，而一些不是。这么设计的原因是，整个编译流水线其实并不需要汇编这一步。相反，编译器作用在半抽象化的指令集，指令选择发生在代码生成阶段。汇编器作用于半抽象指令集，所以当你看到一条指令例如 MOV，最终的指令也许并不是一条 move 指令，也许是 clear 或 load 指令。也许直接映射到一条真实的 move 机器指令。通常情况下，特定机器的操作会直接映射到机器指令，然而一些通用的概念，例如内存移动，子过程调用和返回等，要更加抽象些。具体细节因架构而异，我们对这种不精确性感到抱歉，这些情况没有被很好的定义。

> 汇编程序完成半抽象的指令集到具体机器指令的转换，连接程序完成汇编程序输出的链接。

## 分解一个简单的程序

考虑下面的 Go 代码 ([direct_topfunc_call.go](./direct_topfunc_call.go)):
```Go
//go:noinline
func add(a, b int32) (int32, bool) { return a + b, true }

func main() { add(10, 32) }
```
*(请注意 `//go:noinline` 编译器指示符... 请勿担心。)*

我们编译代码得到下面的汇编：
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

我们接下来通过按行分解这两个函数，来更好的理解编译器都做了那些动作。

### `add` 解析

```Assembly
0x0000 TEXT "".add(SB), NOSPLIT, $0-16
```

- `0x0000`: 当前指令相对于函数开始地址的偏移量。

- `TEXT "".add`: `TEXT` 指示符声明 `"".add` 符号属于 `.text` 段 (可执行代码)，后面的指令属于当前函数。
`""` 在链接时会被替换成当前包名字符串，例如 `"".add` 在最终链接生成的可执行代码里面会变成 `main.add`。

- (SB)`: `SB` 是一个虚拟寄存器，存放“静态基地址”指针，例如，我们程序运行时地址空间的开始地址。
`"".add(SB)` 声明当前符号地址相对于地址空间开始地址是一个常量（由连接器计算得出）。换句话说，这是一个全局函数符号，它有一个绝对引用地址。可以用 `objdump` 命令验证这点。

```
$ objdump -j .text -t direct_topfunc_call | grep 'main.add'
000000000044d980 g     F .text	000000000000000f main.add
```

> 所有用户自定义的符号都表示为相对于伪寄存器 FP（函数参数和局部变量）和 SB（函数和全局变量）的偏移量。
> 伪寄存器 SB 可以当作内存起始地址，符号 foo(SB) 即是对名为 foo 内存地址的引用。

- `NOSPLIT`：指示编译器*不要*插入 *stack-split* 检查指令，这些指令用于检查当前栈是否需要扩展。
以本例中 `add` 函数来说，编译器自己已经判断出来 `add` 函数没有局部变量，它没有发生栈溢出的风险，所以编译器自己就设置了这个 `NOSPLIT` 指示符，防止 CPU 在每次 `add` 函数被调用是做一些无用功。

> "NOSPLIT"：指示编译器不要插入代码前言（preamble）来检查栈是否需要切分。当前函数加上它调用的所有函数的栈帧，需要确保不会超过剩余可用栈空间。该指示符用来保护栈切分代码的函数栈（译者注：否则逻辑上栈切分代码可以触发栈切分）。
在本章的结尾处，我们对 goroutine 和 stack-splits 有简短的介绍。

- `$0-16`：`$0` 表示当前函数以字节为单位的栈帧大小，`$16` 表示调用函数传入参数占用空间大小。
> 通常情况下，栈帧的大小后面附加着参数的大小，用 `-` 符号分隔。（这仅仅是惯用约定语法，不表示减法操作。）栈帧大小 $24-8 表示函数需要24字节的栈帧空间，调用函数会传入8字节大小的参数，这个参数空间计算在调用这栈帧内。如果 TEXT 没有 NOSPLIT 指示符，参数大小必须提供。Go 工具链 vet 可以用来检测确保汇编函数签名遵守这个规范。

```Assembly
0x0000 FUNCDATA $0, gclocals·f207267fbf96a0178e8758c6e3e0ce28(SB)
0x0000 FUNCDATA $1, gclocals·33cdeccccebe80329f1fdbee7f5874cb(SB)
```

> FUNCDATA 和 PCDATA 指示符是由编译器引入，辅助垃圾回收器实现垃圾回收。

目前请不用关心，我们会在垃圾回收章节对它们做详细的阐述。

```Assembly
0x0000 MOVL "".b+12(SP), AX
0x0004 MOVL "".a+8(SP), CX
```

Go 语言的调用约定强制要求每个参数使用调用者函数预留的栈帧空间在栈上传递。
调用者函数负责根据情况增长（或者收缩）栈，确保被调用函数参数和返回值空间。

Go 编译器不会生成类似 PUSH/POP 栈操作指令：栈的增长和缩小是通过减小或者增加虚拟硬件栈指针寄存器 `SP`。
*[更新: 我们对这个问题有过 Issue 讨论 [issue #21: about SP register](https://github.com/teh-cmc/go-internals/issues/21).]*
> `SP` 伪寄存器是一个虚拟栈帧指针，用来引用帧局部变量和调用函数的参数。它指向局部栈帧的顶部，它的范围是 [-framesize, 0)：引用形式一般是 x-8(SP), y-4(SP) 偏移为负数。

尽管 Go 官方文档声明"*所有用户定义的符号都被写作相对于伪寄存器 FP（参数和局部变量）的偏移*"，但这仅仅对于手写汇编代码是成立的。
拿最近的 Go 编译器来说，Go 工具链生成代码常常使用相对于栈指针偏移量来引用参数和局部变量。这样的好处是对于那些寄存器比较少的平台（例如 x86），帧指针寄存器就可以被用作通用寄存器。
如果你对具体的细节感兴趣，请参考本章结尾的链接 *x86-64 栈帧布局*。
*[更新: 我们对这个问题有过 Issue 讨论 [issue #2: Frame pointer](https://github.com/teh-cmc/go-internals/issues/2).]*

`"".b+12(SP)` 和 `"".a+8(SP)` 分别表示栈顶往栈底方向偏移12字节和8字节处（请记住：栈是向下增长的！）。
`.a` and `.b` 表示被引用地址的别名，当使用虚拟寄存器相对地址时，这种语法是强制要求的，尽管*他们没有绝对的语义意义*。

官方文档关于虚拟帧指针有如下描述：
> FP 伪寄存器是一个虚拟帧指针，用于引用函数参数。编译器负责维护虚拟帧指针，并且使用相对于帧指针寄存器的偏移量来引用函数参数。因此 0(FP) 是第一个参数，8(FP) 是第二个参数（64位机器），以此类推。当这样引用函数参数的时候，有必要将参数名称放在前面，例如 first_arg+0(FP) 和 second_arg+8(FP)。（偏移量 -offset 表示相对于帧指针的偏移，这与前文 SB 方式不同，后者表示相对于符号的偏移。）汇编器限制这种约定形式，0(FP) 和 8(FP) 方式会被拒绝。实际的名称在语法上没有强制的相关性，但是可以用来注释参数的意义。

最后，两个重要的事情需要引起注意：
1. 第一个参数 `a` 不是在 `0(SP)`，而是在 `8(SP)`；这是因为调用函数通过 `CALL` 伪指令将返回地址存放在 `0(SP)` 处。
2. 参数传递顺序是从后往前，也就是说第一个参数最靠近栈的顶部。

```Assembly
0x0008 ADDL CX, AX
0x000a MOVL AX, "".~r2+16(SP)
0x000e MOVB $1, "".~r3+20(SP)
```

`ADDL` 对两个长字（例如4字节值）分别存放在 `AX` 和 `CX` 寄存器做加法运算，结果存放在 `AX` 寄存器。
然后加法结果被复制到 `"".~r2+16(SP)` 处，这里调用函数已经预先保留了足够的空间，并且也假定返回值存放在该处。另外，`"".~r2` 在这里没有语义含义。

为了演示 Go 对多返回值的处理，我们附带返回了一个常量 `true`。
多返回值的运行机制和我们第一个返回值一样，仅仅是相对 `SP` 偏移量不同而已。

```Assembly
0x0013 RET
```

最后的 `RET` 伪指令指示 Go 汇编器插入特定平台指令来处理子过程调用的返回。
大部分情况下，它完成将 `0(SP)` 处的返回地址弹出，然后跳转到该处继续执行。

> TEXT 代码块的最后一条指令必须是一条类似跳转的指令，通常是 `RET` 伪指令。
> （如果没有跳转指令，连接器会添加一条跳转到函数自己的指令，不会自动跳转到 TEXT 代码块的其他地方。）

下面是以上部分的汇总：
```Assembly
;; 声明一个全局函数符号 "".add （确切的说当链接后是 main.add）
;; 不要插入栈切分检查代码
;; 函数栈帧大小0字节，传入参数16字节
;; func add(a, b int32) (int32, bool)
0x0000 TEXT	"".add(SB), NOSPLIT, $0-16
  ;; ...省略 FUNCDATA stuff...
  0x0000 MOVL	"".b+12(SP), AX	    ;; 从调用函数栈帧移动第二个长字（4字节）参数到 AX
  0x0004 MOVL	"".a+8(SP), CX	    ;; 从调用函数栈帧移动第一个长字（4字节）参数到 CX
  0x0008 ADDL	CX, AX		        ;; 计算 AX=CX+AX
  0x000a MOVL	AX, "".~r2+16(SP)   ;; 从 AX 移动结果到调用函数栈帧
  0x000e MOVB	$1, "".~r3+20(SP)   ;; 移动常量 `true` 到调用函数栈帧
  0x0013 RET			            ;; 跳转至 `0(SP)` 处存放的函数返回地址
```

下面是 `main.add` 完成执行时的栈示意图：
```
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
<!-- https://textik.com/#af55d3485eaa56f2 -->

### `main` 解析

为了节省你前后翻页时间，这里是 `main` 函数的拷贝：
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

一切如旧：
- `"".main` (链接后是 `main.main`) 是一个 `.text` 代码段的全局函数符号，它的地址是一个相对程序运行时地址空间的常量。
- 它分配24字节的栈帧空间，没有传入参数，也没有返回值。

```Assembly
0x000f SUBQ     $24, SP
0x0013 MOVQ     BP, 16(SP)
0x0018 LEAQ     16(SP), BP
```

如上文所述，Go 语言的调用约强制要求所有参数通过栈传递。

我们的调用函数，`main`，通过减小虚拟栈指针将它的栈帧增大24字节（*注意栈是向下增长，所以 `SUBQ` 指令是增大栈帧*）。
这24字节如下：
- 8 字节 (`16(SP)`-`24(SP)`) 存放当前帧指针 `BP`，用于栈回溯和调试
- 1+3 字节 (`12(SP)`-`16(SP)`) 保留用于第二个返回值 （`bool`）加上 `amd64` 3字节的对齐空间
- 4 字节 (`8(SP)`-`12(SP)`) 保留用于第一个返回值 （`int32`）
- 4 字节 (`4(SP)`-`8(SP)`) 保留用于参数 `b (int32)`
- 4 字节 (`0(SP)`-`4(SP)`) 保留用于参数 `a (int32)`

最后，`LEAQ` 计算新的栈帧地址，存放在 `BP`。

```Assembly
0x001d MOVQ     $137438953482, AX
0x0027 MOVQ     AX, (SP)
```

调用函数将2个参数组合成一个双字（8字节）一次性存放在栈的顶部。
虽然看起来 `137438953482` 像一些随机数据，其实它是 `10` 和 `32` 2个4字节值组合成一个8字节值：
```
$ echo 'obase=2;137438953482' | bc
10000000000000000000000000000000001010
\____/\______________________________/
   32                              10
```

```Assembly
0x002b CALL     "".add(SB)
```

我们通过 `CALL` 相对静态基地址偏移来调用 `add` 函数：这种跳转是直接地址跳转。

`CALL` 指令会将返回地址（8字节值）保存到栈顶，所以 `add` 函数每次引用 `SP` 都需要考虑这8个字节！
例如，`"".a` 地址不是 `0(SP)`，而是 `8(SP)`。

```Assembly
0x0030 MOVQ     16(SP), BP
0x0035 ADDQ     $24, SP
0x0039 RET
```

最后，返回前需要做如下准备:
1. 将帧指针设置为调用函数帧指针
2. 修改 `SP` 寄存器，回收我们分配的24字节栈空间
3. Go 汇编器插入其他子过程返回相关处理指令

## goroutines, stacks and splits 介绍

当前我们不会过多的涉及 goroutine 的内部细节（*..后续会介绍*），当我们接触越来越多的汇编，你会发现栈的管理指令会越来越常见。
到时我们能够快速的识别出这些栈指令，理解他们在做什么以及为什么这么做。

### Stacks

由于 goroutine 的数量是动态变化的，实际应用中可能达到百万级别的数量，当分配 goroutine 栈空间的时候 Go 运行时必须采取保守的方式。
因此，每个 goroutine 初始时栈空间默认是 2KB（栈空间是从堆空间分配的）。

当 goroutine 运行过程中，它有可能超出初始栈空间（即栈溢出）。
为了防止栈溢出，Go 运行时确保在 goroutine 栈溢出前，分配一个2倍于当前栈大小的新栈，并且拷贝当前栈内容到新栈。
这个过程就是熟知的 *stack-split* 技术，实现 goroutine 栈大小的动态伸缩。

### Splits

为实现 *stack-split*，编译器在每个可能发生栈溢出的函数开始和结束插入少量的指令。
正如在本章前面所见，为了避免不必要的开销，不可能发生栈溢出的函数被标记 `NOSPLIT` 指示符，提示编译器不要插入相关检查。

我们再回头看前面的主函数，这次启用了 stack-split 前言(preamble)：
```Assembly
0x0000 TEXT	"".main(SB), $24-0
  ;; stack-split 序言(prologue)
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

  ;; stack-split 结语(epilogue)
  0x003a NOP
  ;; ...omitted PCDATA stuff...
  0x003a CALL	runtime.morestack_noctxt(SB)
  0x003f JMP	0
```

正如你所见，stack-split 前言 (preamble) 分为序言 (prologue) 和结语 (epilogue)
- 序言 (prologue) 检查 goroutine 是否将要栈溢出，如果是，跳转到结语 (epilogue) 处。
- 结语 (epilogue) 触发栈增长相关程序处执行，然后跳转到序言 (prologue)。

这样就创建了一个反馈环，确保处于饥饿状态的 goroutine 拥有足够的栈空间。

**Prologue**
```Assembly
0x0000 MOVQ	(TLS), CX   ;; 保存当前 *g 到 CX
0x0009 CMPQ	SP, 16(CX)  ;; 比较 SP 和 g.stackguard0
0x000d JLS	58	        ;; 如果 SP <= g.stackguard0，跳转到 0x3a 处
```

`TLS` 是一个由运行时维护的虚拟寄存器，它存放指向当前运行 goroutine `g` 的指针。`g` 数据结构保存了 goroutine 所有的运行状态信息。

请看 Go 运行时源码关于 `g` 的定义：
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
我们能够得出 `16(CX)` 指向 `g.stackguard0`，这个值就是运行时维护的当前栈空间阈值，通过与它比较 stack-pointer，就可以知道当前 goroutine 是否将会发生栈溢出。
序言 (prologue) 通过比较当前 `SP` 与 `stackguard0`，如果大于情况，就跳转到结语 (epilogue) 处。

**Epilogue**
```Assembly
0x003a NOP
0x003a CALL	runtime.morestack_noctxt(SB)
0x003f JMP	0
```

结语 (epilogue) 代码很清楚，它调用 Go 运行时代码，这些代码负责处理栈增长逻辑，完成后，返回到序言 (prologue) 的第一条指令处。

`CALL` 指令前的 `NOP` 指令，是为了处理某些平台的特殊限制问题。通常的做法就是在实际指令执行前执行一些 `NOP` 指令。
*[更新: 我们对此有过 Issue 讨论 [issue #4: Clarify "nop before call" paragraph](https://github.com/teh-cmc/go-internals/issues/4).]*

### 省略的细节

我们目前仅仅只是是对冰山一角瞥了一眼。
我们并没有提及栈增长的内部实现机制许多的微妙之处，整个过程得花一整章的内容来阐述。

我们会在适当的时候再来回顾这些细节。

## 总结

这章 Go 汇编语言的快速介绍应该能够帮助你开始了。

随着本书越来越深入 Go 语言的内部，Go 汇编会成为我们理解底层和弄清复杂细节最值得信赖的工具。

如果你有任何疑问和建议，请给我们提 Issue，开头请用 `chapter1:`！

## 链接

- [[Official] A Quick Guide to Go's Assembler](https://golang.org/doc/asm)
- [[Official] Go Compiler Directives](https://golang.org/cmd/compile/#hdr-Compiler_Directives)
- [[Official] The design of the Go Assembler](https://www.youtube.com/watch?v=KINIAgRpkDA)
- [[Official] Contiguous stacks Design Document](https://docs.google.com/document/d/1wAaf1rYoM4S4gtnPh0zOlGzWtrZFQ5suE8qr2sD8uWQ/pub)
- [[Official] The `_StackMin` constant](https://github.com/golang/go/blob/bf86aec25972f3a100c3aa58a6abcbcc35bdea49/src/runtime/stack.go#L70-L71)
- [[Discussion] Issue #2: *Frame pointer*](https://github.com/teh-cmc/go-internals/issues/2)
- [[Discussion] Issue #4: *Clarify "nop before call" paragraph*](https://github.com/teh-cmc/go-internals/issues/4)
- [A Foray Into Go Assembly Programming](https://blog.sgmansfield.com/2017/04/a-foray-into-go-assembly-programming/)
- [Dropping Down Go Functions in Assembly](https://www.youtube.com/watch?v=9jpnFmJr2PE)
- [What is the purpose of the EBP frame pointer register?](https://stackoverflow.com/questions/579262/what-is-the-purpose-of-the-ebp-frame-pointer-register)
- [Stack frame layout on x86-64](https://eli.thegreenplace.net/2011/09/06/stack-frame-layout-on-x86-64)
- [How Stacks are Handled in Go](https://blog.cloudflare.com/how-stacks-are-handled-in-go/)
- [Why stack grows down](https://gist.github.com/cpq/8598782)
