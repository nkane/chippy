//go:build nessy

package main

import (
	"io"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"

	"github.com/nkane/chippy/internal/nes/apu"
)

// apuStream is an io.Reader that pulls samples from the APU's ring
// buffer and reshapes them into Ebiten's expected PCM layout:
// 16-bit signed little-endian *stereo* (the APU emits mono, so each
// sample duplicates across L/R). On a read shortfall — the CPU
// hasn't generated enough samples yet for the audio thread's
// hunger — apuStream pads with silence so the audio context never
// stalls.
//
// Ebiten calls Read on its own goroutine; cpuMu is shared with the
// game-loop and DAP-server goroutines so APU.Samples() observes a
// consistent view of the channel state.
type apuStream struct {
	apu     *apu.APU
	cpuMu   *sync.Mutex
	pending []byte // unflushed bytes left over from the previous Read
}

// Read fills p with stereo PCM bytes. Pads with silence if the APU
// ring hasn't produced enough samples to satisfy the request — keeps
// the audio thread from blocking on an under-fed CPU.
func (s *apuStream) Read(p []byte) (int, error) {
	for len(s.pending) < len(p) {
		s.cpuMu.Lock()
		mono := s.apu.Samples()
		s.cpuMu.Unlock()
		if len(mono) == 0 {
			break
		}
		// Each mono int16 becomes 4 bytes of stereo PCM (L+R little-
		// endian). Grow pending in one allocation per drain pass.
		s.pending = append(s.pending, make([]byte, len(mono)*4)...)
		dst := s.pending[len(s.pending)-len(mono)*4:]
		for i, sample := range mono {
			lo, hi := byte(sample), byte(sample>>8)
			off := i * 4
			dst[off+0] = lo
			dst[off+1] = hi
			dst[off+2] = lo
			dst[off+3] = hi
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	// Pad shortfall with silence so the audio context keeps polling.
	if n < len(p) {
		for i := n; i < len(p); i++ {
			p[i] = 0
		}
		n = len(p)
	}
	return n, nil
}

// audioSink owns the Ebiten audio Context + Player tied to the APU.
// Disabled (nil sink) when -mute is set or when the host platform
// can't open an audio device; the rest of the program runs fine
// without it.
type audioSink struct {
	ctx    *audio.Context
	player *audio.Player
}

// newAudioSink wires the APU's sample ring into Ebiten's audio
// pipeline. Returns nil if `mute` is true so the caller can no-op
// cleanly. The Context follows Ebiten's process-singleton rule —
// at most one per process — so any future audio source (e.g. UI
// chimes) must reuse this one.
func newAudioSink(a *apu.APU, cpuMu *sync.Mutex, mute bool) (*audioSink, error) {
	if mute {
		return nil, nil
	}
	ctx := audio.NewContext(apu.SampleRate)
	stream := &apuStream{apu: a, cpuMu: cpuMu}
	player, err := ctx.NewPlayer(io.Reader(stream))
	if err != nil {
		return nil, err
	}
	return &audioSink{ctx: ctx, player: player}, nil
}

// start kicks off playback. Called once after newGame so the
// player is ready before the game loop's first tick.
func (s *audioSink) start() {
	if s == nil {
		return
	}
	s.player.Play()
}

// close stops + releases the player. Best-effort; errors during
// shutdown aren't actionable.
func (s *audioSink) close() {
	if s == nil || s.player == nil {
		return
	}
	_ = s.player.Close()
}
