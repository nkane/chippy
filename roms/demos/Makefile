# roms/demos/Makefile — builds nessy's homemade demo ROMs.
#
# Uses cc65's ca65 assembler + ld65 linker. Each demo lives in its
# own subdirectory with a .s source and a .cfg linker config. The
# built .nes is committed to the repo so tests + users don't need
# the toolchain to run them; this Makefile is only invoked when a
# demo source changes.
#
# Targets:
#   make all            — build every demo
#   make hello-bg       — build hello-bg only
#   make clean          — remove .o, .map, and built .nes
#
# Toolchain (cc65 ≥ 2.19) on macOS:  brew install cc65
#               on debian/ubuntu:    sudo apt-get install cc65

DEMOS := hello-bg input-echo vblank-bounce

CA65      := ca65
LD65      := ld65
CA65FLAGS := -t none
LD65FLAGS :=

.PHONY: all clean $(DEMOS)

all: $(DEMOS)

# Per-demo rule emitter. Generates a phony target named after the demo
# (e.g. `make hello-bg`) plus the underlying .o → .nes chain.
define demo_rule
$(1): $(1)/$(1).nes

$(1)/$(1).nes: $(1)/$(1).s $(1)/$(1).cfg
	$$(CA65) $$(CA65FLAGS) -o $(1)/$(1).o $(1)/$(1).s
	$$(LD65) $$(LD65FLAGS) -C $(1)/$(1).cfg -m $(1)/$(1).map -o $(1)/$(1).nes $(1)/$(1).o
endef

$(foreach d,$(DEMOS),$(eval $(call demo_rule,$(d))))

clean:
	$(RM) */*.o */*.map */*.nes
