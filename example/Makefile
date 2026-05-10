# Build the example programs with cc65.
#
# All programs share load_five.cfg (ROM $8000-$FFFF, reset vector at $FFFC).
#
# Targets:
#   make            -> build every .bin (+ .dbg symbol file)
#   make run        -> build then launch chippy on load_five.bin
#   make run-<name> -> build then launch chippy on <name>.bin
#                       e.g. make run-fibonacci
#   make clean      -> remove build artifacts

CFG := load_five.cfg

PROGRAMS := load_five count_to_ten fibonacci stack_demo bcd_add cmos_demo

OBJS := $(PROGRAMS:=.o)
BINS := $(PROGRAMS:=.bin)
DBGS := $(PROGRAMS:=.dbg)

CHIPPY := go run ../cmd/chippy

.PHONY: all run clean $(addprefix run-,$(PROGRAMS))

all: $(BINS)

%.o: %.s
	ca65 -g $< -o $@

# 65C02 demo needs the CMOS instruction set selected at assembly time.
cmos_demo.o: cmos_demo.s
	ca65 --cpu 65c02 -g $< -o $@

%.bin: %.o $(CFG)
	ld65 -C $(CFG) -o $@ --dbgfile $*.dbg $<

run: load_five.bin
	$(CHIPPY) -rom load_five.bin

# 65C02 demo needs the matching CPU variant at runtime.
run-cmos_demo: cmos_demo.bin
	$(CHIPPY) --cpu 65c02 -rom cmos_demo.bin

run-%: %.bin
	$(CHIPPY) -rom $<

clean:
	rm -f $(OBJS) $(BINS) $(DBGS)
