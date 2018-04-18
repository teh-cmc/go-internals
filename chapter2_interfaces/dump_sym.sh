#!/usr/bin/env bash

BIN="$1"
test "$BIN"
SECTION="$2"
test "$SECTION"
SYM="$3"
test "$SYM"

section_offset=$(
  readelf -St -W "$BIN" | \
  grep -A 1 "$SECTION" | \
  tail -n +2 | \
  awk '{print toupper($3)}'
)
section_offset_dec=$(echo "ibase=16;$section_offset" | bc)
echo "$SECTION file-offset: $section_offset_dec"

section_vma=$(
  readelf -St -W "$BIN" | \
  grep -A 1 "$SECTION" | \
  tail -n +2 | \
  awk '{print toupper($2)}'
)
section_vma_dec=$(echo "ibase=16;$section_vma" | bc)
echo "$SECTION VMA: $section_vma_dec"

sym_vma=$(objdump -t -j "$SECTION" "$BIN" | grep "$SYM" | awk '{print toupper($1)}')
sym_vma_dec=$(echo "ibase=16;$sym_vma" | bc)
echo "$SYM VMA: $sym_vma_dec"
sym_size=$(objdump -t -j "$SECTION" "$BIN" | grep "$SYM" | awk '{print toupper($5)}')
sym_size_dec=$(echo "ibase=16;$sym_size" | bc)
echo -e "$SYM SIZE: $sym_size_dec\n"

sym_offset=$(( $sym_vma_dec - $section_vma_dec + $section_offset_dec ))
dd if="$BIN" of=/dev/stdout bs=1 count=$sym_size_dec skip="$sym_offset" 2>/dev/null | hexdump
