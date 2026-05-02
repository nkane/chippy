# Build the example program with cc65.
#
# Targets:
#   make            -> build load_five.bin (+ .dbg symbol file)
#   make run        -> build then launch chippy on the binary
#   make clean      -> remove build artifacts

SRC := load_five.s
CFG := load_five.cfg
OBJ := $(SRC:.s=.o)
BIN := $(SRC:.s=.bin)
DBG := $(SRC:.s=.dbg)

CHIPPY := go run ../cmd/chippy

.PHONY: all run clean

all: $(BIN)

$(OBJ): $(SRC)
	ca65 -g $< -o $@

$(BIN): $(OBJ) $(CFG)
	ld65 -C $(CFG) -o $(BIN) --dbgfile $(DBG) $(OBJ)

run: $(BIN)
	$(CHIPPY) -rom $(BIN)

clean:
	rm -f $(OBJ) $(BIN) $(DBG)
