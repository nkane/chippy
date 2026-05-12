// fizzbuzz.c — classic interview filler, output to chippy's TextOutput.
//
// Build:
//   make fizzbuzz.bin
// Run:
//   chippy -rom fizzbuzz.bin
//
// Demonstrates: integer modulo, string concatenation, and a tiny
// itoa for the bare-metal case where stdio isn't linked.

#include <stdint.h>

static volatile uint8_t* const tx = (uint8_t*)0xF001;

static void putc(char c) { *tx = c; }

static void puts(const char *s) {
    while (*s) {
        putc(*s++);
    }
}

// Print decimal value 1..99. Caller guarantees range so we don't pull
// in a generic itoa.
static void put_u8(uint8_t n) {
    if (n >= 10) {
        putc('0' + (n / 10));
    }
    putc('0' + (n % 10));
}

int main(void) {
    uint8_t i;
    for (i = 1; i <= 15; i++) {
        if (i % 15 == 0) {
            puts("fizzbuzz");
        } else if (i % 3 == 0) {
            puts("fizz");
        } else if (i % 5 == 0) {
            puts("buzz");
        } else {
            put_u8(i);
        }
        putc('\n');
    }
    return 0;
}
