# Build the C example programs with cc65 + ca65 + ld65.
#
# Each .c is compiled to a .s by cc65, assembled to a .o by ca65, then
# linked against crt0.o and chippy.cfg by ld65. The output is a flat
# 64 KiB binary chippy loads at $8000.
#
# Targets:
#   make            -> build every .bin
#   make run-<name> -> build then launch chippy on <name>.bin
#                       e.g. make run-hello, make run-fizzbuzz
#   make clean      -> remove build artifacts

CFG := chippy.cfg
CRT0 := crt0.o

PROGRAMS := hello sum fizzbuzz

BINS := $(PROGRAMS:=.bin)
SS   := $(PROGRAMS:=.s)
OBJS := $(PROGRAMS:=.o)
DBGS := $(PROGRAMS:=.dbg)

CHIPPY := go run ../../cmd/chippy

.PHONY: all clean $(addprefix run-,$(PROGRAMS))

all: $(BINS)

# Disable make's built-in rules so its default .c -> .o (host cc)
# doesn't shadow our cc65 + ca65 pipeline.
MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

# C -> assembly via cc65 (-g keeps source-line debug info in the .s).
%.s: %.c
	cc65 -t none -g -O $< -o $@

# Assembly -> object via ca65.
%.o: %.s
	ca65 -g $< -o $@

# crt0 is shared by every program.
$(CRT0): crt0.s
	ca65 -g crt0.s -o $(CRT0)

# Link program object + crt0 against the chippy linker config. The
# cc65 runtime library (none.lib) provides zerobss, copydata, initlib,
# donelib, sp, and the soft-stack helpers (pusha, pushax, etc.).
%.bin: %.o $(CRT0) $(CFG)
	ld65 -C $(CFG) -o $@ --dbgfile $*.dbg $(CRT0) $*.o none.lib

run-%: %.bin
	$(CHIPPY) -rom $<

clean:
	rm -f $(OBJS) $(SS) $(BINS) $(DBGS) $(CRT0)
