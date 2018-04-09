package main

var j uint32
var Eface interface{} // outsmart compiler (avoid static inference)

func assertion() {
	i := uint32(42)
	Eface = i

	// 0x0065 00101 MOVQ  "".Eface(SB), AX          ;; AX = Eface._type
	// 0x006c 00108 MOVQ  "".Eface+8(SB), CX        ;; CX = Eface.data
	// 0x0073 00115 LEAQ  type.uint32(SB), DX       ;; DX = type.uint32
	// 0x007a 00122 CMPQ  AX, DX                    ;; Eface._type == type.uint32 ?
	// 0x007d 00125 JNE   162                       ;; no? panic our way outta here
	// 0x007f 00127 MOVL  (CX), AX                  ;; AX = *Eface.data
	// 0x0081 00129 MOVL  AX, "".j(SB)              ;; j = AX = *Eface.data
	// ;; exit
	// 0x0087 00135 MOVQ  40(SP), BP
	// 0x008c 00140 ADDQ  $48, SP
	// 0x0090 00144 RET
	// ;; panic: interface conversion: <iface> is <have>, not <want>
	// 0x00a2 00162 MOVQ  AX, (SP)                  ;; have: Eface._type
	// 0x00a6 00166 MOVQ  DX, 8(SP)                 ;; want: type.uint32
	// 0x00ab 00171 LEAQ  type.interface {}(SB), AX ;; AX = type.interface{} (eface)
	// 0x00b2 00178 MOVQ  AX, 16(SP)                ;; iface: AX
	// 0x00b7 00183 CALL  runtime.panicdottypeE(SB) ;; func panicdottypeE(have, want, iface *_type)
	// 0x00bc 00188 UNDEF
	// 0x00be 00190 NOP
	j = Eface.(uint32)
}

func typeSwitch() {
	i := uint32(42)
	Eface = i

	// ;; switch v := Eface.(type)
	// 0x0065 00101 MOVQ    "".Eface(SB), AX    ;; AX = Eface._type
	// 0x006c 00108 MOVQ    "".Eface+8(SB), CX  ;; CX = Eface.data
	// 0x0073 00115 TESTQ   AX, AX              ;; Eface._type == nil ?
	// 0x0076 00118 JEQ     153                 ;; yes? exit the switch
	// 0x0078 00120 MOVL    16(AX), DX          ;; DX = Eface.type._hash
	// ;; case uint32
	// 0x007b 00123 CMPL    DX, $-800397251     ;; Eface.type._hash == type.uint32.hash ?
	// 0x0081 00129 JNE     163                 ;; no? go to next case (uint16)
	// 0x0083 00131 LEAQ    type.uint32(SB), BX ;; BX = type.uint32
	// 0x008a 00138 CMPQ    BX, AX              ;; type.uint32 == Eface._type ? (HASH COLLISION?)
	// 0x008d 00141 JNE     206                 ;; no? clear BX and go to next case (uint16)
	// 0x008f 00143 MOVL    (CX), BX            ;; BX = *Eface.data
	// 0x0091 00145 JNE     163                 ;; landsite for indirect jump starting at 0x00d3
	// 0x0093 00147 MOVL    BX, "".j(SB)        ;; j = BX = *Eface.data
	// ;; exit
	// 0x0099 00153 MOVQ    40(SP), BP
	// 0x009e 00158 ADDQ    $48, SP
	// 0x00a2 00162 RET
	// ;; case uint16
	// 0x00a3 00163 CMPL    DX, $-269349216     ;; Eface.type._hash == type.uint16.hash ?
	// 0x00a9 00169 JNE     153                 ;; no? exit the switch
	// 0x00ab 00171 LEAQ    type.uint16(SB), DX ;; DX = type.uint16
	// 0x00b2 00178 CMPQ    DX, AX              ;; type.uint16 == Eface._type ? (HASH COLLISION?)
	// 0x00b5 00181 JNE     199                 ;; no? clear AX and exit the switch
	// 0x00b7 00183 MOVWLZX (CX), AX            ;; AX = uint16(*Eface.data)
	// 0x00ba 00186 JNE     153                 ;; landsite for indirect jump starting at 0x00cc
	// 0x00bc 00188 MOVWLZX AX, AX              ;; AX = uint16(AX) (redundant)
	// 0x00bf 00191 MOVL    AX, "".j(SB)        ;; j = AX = *Eface.data
	// 0x00c5 00197 JMP     153                 ;; we're done, exit the switch
	// ;; indirect jump table
	// 0x00c7 00199 MOVL    $0, AX              ;; AX = $0
	// 0x00cc 00204 JMP     186                 ;; indirect jump to 153 (exit)
	// 0x00ce 00206 MOVL    $0, BX              ;; BX = $0
	// 0x00d3 00211 JMP     145                 ;; indirect jump to 163 (case uint16)
	switch v := Eface.(type) {
	case uint16:
		j = uint32(v)
	case uint32:
		j = v
	}
}

func main() {
	assertion()
	typeSwitch()
}
