//go:build nessy

package main

import (
	"io"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"

	"github.com/nkane/chippy/internal/nes/apu"
)

// apuStream is an io.Reader Ebiten's audio Player pulls PCM from on
// its own goroutine. Decoupled from cpuMu (issue / pprof: the audio
// thread used to drain APU.Samples() under cpuMu and spent ~38% of
// runtime in pthread_cond_signal contending with the game loop's
// 16ms Update). Now the game loop pushes ready stereo bytes into a
// per-stream queue under a dedicated mutex; the audio thread only
// touches that queue.
//
// Format: 16-bit signed little-endian *stereo* (mono APU output
// duplicated across L/R). Pads short reads with silence so the
// player keeps polling instead of stalling.
type apuStream struct {
	mu      sync.Mutex
	pending []byte
}

// maxQueueBytes caps the pending queue at ~150ms of stereo PCM
// (44100 samples/sec × 4 bytes × 0.15 s ≈ 26 KB). 80ms was too
// tight — Ebiten's audio thread jitter occasionally pushed past
// the cap, causing audible drops ("scan-like click"). 150ms
// absorbs the jitter while keeping perceived input-to-sound
// latency under ~200ms (Player buffer 50ms + queue worst case
// 150ms). Drops only on pathological stalls (paused debugger).
const maxQueueBytes = 4 * 44100 * 150 / 1000

// Push appends new stereo PCM bytes. Called from the game-loop
// goroutine after it drains APU.Samples() under cpuMu — we copy /
// reshape outside the cpuMu critical section so the audio thread
// never races against the game's per-frame Step batch.
func (s *apuStream) Push(stereo []byte) {
	s.mu.Lock()
	s.pending = append(s.pending, stereo...)
	if over := len(s.pending) - maxQueueBytes; over > 0 {
		// Drop oldest bytes, keep latest. Aligned drop on 4-byte
		// stereo-sample boundary to avoid mid-sample garbage.
		over -= over % 4
		if over > 0 {
			s.pending = s.pending[over:]
		}
	}
	s.mu.Unlock()
}

// Read fills p with whatever's queued; silence-pads any shortfall.
// Never blocks waiting for the producer.
func (s *apuStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	s.mu.Unlock()
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
	stream *apuStream
}

// newAudioSink wires the APU's sample ring into Ebiten's audio
// pipeline. Returns nil if `mute` is true so the caller can no-op
// cleanly. The Context follows Ebiten's process-singleton rule —
// at most one per process — so any future audio source (e.g. UI
// chimes) must reuse this one.
func newAudioSink(mute bool) (*audioSink, error) {
	if mute {
		return nil, nil
	}
	ctx := audio.NewContext(apu.SampleRate)
	stream := &apuStream{}
	player, err := ctx.NewPlayer(io.Reader(stream))
	if err != nil {
		return nil, err
	}
	// Shrink the Player's internal buffer to ~50ms. Default is
	// closer to 100ms on macOS which compounds with our queue
	// depth to give 200+ ms of perceptible delay between a button
	// press + the resulting sound. NES players notice <50ms; we
	// aim for sub-100ms total end-to-end.
	player.SetBufferSize(50 * time.Millisecond)
	return &audioSink{ctx: ctx, player: player, stream: stream}, nil
}

// start kicks off playback. Called once after newGame so the
// player is ready before the game loop's first tick. Pre-seeds
// the queue with a short slice of silence so the audio thread's
// initial Read finds something to consume before the first frame
// of game audio arrives — without that prime, oto starts in an
// "underrun" state and emits a click on first real data.
func (s *audioSink) start() {
	if s == nil {
		return
	}
	const primeBytes = 4 * 44100 * 20 / 1000 // ~20ms of silence
	s.stream.Push(make([]byte, primeBytes))
	s.player.Play()
}

// push reshapes APU mono samples into stereo PCM + enqueues them
// for the audio thread. Called from the game loop right after each
// per-frame CPU step batch (inside the cpuMu critical section, but
// the queue mutation itself drops cpuMu and grabs the stream's own
// mu — no cross-thread contention).
func (s *audioSink) push(mono []int16) {
	if s == nil || len(mono) == 0 {
		return
	}
	stereo := make([]byte, len(mono)*4)
	for i, sample := range mono {
		lo, hi := byte(sample), byte(sample>>8)
		off := i * 4
		stereo[off+0] = lo
		stereo[off+1] = hi
		stereo[off+2] = lo
		stereo[off+3] = hi
	}
	s.stream.Push(stereo)
}

// close stops + releases the player. Best-effort; errors during
// shutdown aren't actionable.
func (s *audioSink) close() {
	if s == nil || s.player == nil {
		return
	}
	_ = s.player.Close()
}
