// hello.c — write a string to chippy's Apple-1-style text-output
// peripheral at $F001, then halt.
//
// Build:
//   make hello.bin
// Run:
//   chippy -rom hello.bin
//
// The TextOutput peripheral lives in MMIO space. Each byte we write
// to $F001 is appended to chippy's Output panel; chippy itself drains
// the buffer when running in the TUI. No interrupt handling required.

#include <stdint.h>

static volatile uint8_t* const tx = (uint8_t*)0xF001;

static void chippy_putc(char c) { *tx = c; }

static void chippy_puts(const char *s) {
    while (*s) {
        chippy_putc(*s++);
    }
}

int main(void) {
    chippy_puts("hello from c on chippy!\n");
    chippy_puts("the cc65 toolchain works.\n");
    return 0;
}
