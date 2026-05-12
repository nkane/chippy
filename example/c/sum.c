// sum.c — compute 1+2+...+10 and store the result at $0200.
//
// Build:
//   make sum.bin
// Run:
//   chippy -rom sum.bin
// then `:goto $0200` in the TUI to see the result (decimal 55 = $37).

#include <stdint.h>

static volatile uint8_t* const result = (uint8_t*)0x0200;

int main(void) {
    uint8_t total = 0;
    uint8_t i;
    for (i = 1; i <= 10; i++) {
        total += i;
    }
    *result = total;     /* expect $37 at $0200 */
    return 0;
}
