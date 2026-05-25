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

DEMOS := hello-bg input-echo vblank-bounce audio-test \
         triangle-arpeggio noise-drum all-channels dmc-sample \
         mmc1-banks oam-grid state-counter vrc6-chord

CA65      := ca65
LD65      := ld65
# -g emits debug info in the .o file; --dbgfile writes a sibling
# `.dbg` next to each `.nes` so nessy can auto-detect the source map
# (issue #211). The dbg + map files are committed alongside the .nes
# artifact so the source-aware TUI attach works out-of-the-box.
CA65FLAGS := -t none -g
LD65FLAGS :=

.PHONY: all clean $(DEMOS)

all: $(DEMOS)

# Per-demo rule emitter. Generates a phony target named after the demo
# (e.g. `make hello-bg`) plus the underlying .o → .nes chain.
define demo_rule
$(1): $(1)/$(1).nes

$(1)/$(1).nes: $(1)/$(1).s $(1)/$(1).cfg
	$$(CA65) $$(CA65FLAGS) -o $(1)/$(1).o $(1)/$(1).s
	$$(LD65) $$(LD65FLAGS) -C $(1)/$(1).cfg -m $(1)/$(1).map --dbgfile $(1)/$(1).dbg -o $(1)/$(1).nes $(1)/$(1).o
endef

$(foreach d,$(DEMOS),$(eval $(call demo_rule,$(d))))

clean:
	$(RM) */*.o */*.map */*.nes */*.dbg
