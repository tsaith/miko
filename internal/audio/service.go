package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"

	"miko/internal/debuglog"
)

const (
	DefaultSampleRate = 16000
	DefaultChannels   = 1
)

type Service struct {
	context *malgo.AllocatedContext

	mu            sync.Mutex
	captureDevice *malgo.Device
	playbackMu    sync.Mutex
	commandMu     sync.Mutex
	playbackCmd   *exec.Cmd
	stopPlayback  atomic.Bool
	logger        *debuglog.Logger
}

var errNoSystemPlaybackCommand = errors.New("no system playback command available")

func NewService(logger *debuglog.Logger) (*Service, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {})
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	return &Service{context: ctx, logger: logger}, nil
}

func (s *Service) Close() {
	s.StopPlayback()
	s.StopCapture()
	if s.context != nil {
		s.context.Uninit()
		s.context.Free()
		s.context = nil
	}
}

func (s *Service) StartCapture(onChunk func([]byte)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.captureDevice != nil {
		return nil
	}
	if s.context == nil {
		return fmt.Errorf("audio context is not initialized")
	}

	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = DefaultChannels
	config.SampleRate = DefaultSampleRate

	deviceCallbacks := malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			if len(input) == 0 {
				return
			}
			copied := append([]byte(nil), input...)
			onChunk(copied)
		},
	}

	device, err := malgo.InitDevice(s.context.Context, config, deviceCallbacks)
	if err != nil {
		return fmt.Errorf("init capture device: %w", err)
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		return fmt.Errorf("start capture device: %w", err)
	}

	s.captureDevice = device
	return nil
}

func (s *Service) StopCapture() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.captureDevice == nil {
		return
	}
	_ = s.captureDevice.Stop()
	s.captureDevice.Uninit()
	s.captureDevice = nil
}

func (s *Service) PlayPCM(sessionID string, pcm []byte, onLevel func(float64)) error {
	if len(pcm) == 0 {
		return nil
	}

	s.playbackMu.Lock()
	defer s.playbackMu.Unlock()
	s.stopPlayback.Store(false)

	if err := s.playWithSystemPlayer(sessionID, pcm, onLevel); err == nil {
		return nil
	} else if !errors.Is(err, errNoSystemPlaybackCommand) {
		return err
	}
	s.logger.SessionWarnf(sessionID, "audio.playback", "system player unavailable, fallback to malgo")

	if s.context == nil {
		return fmt.Errorf("audio context is not initialized")
	}
	s.logger.SessionInfof(sessionID, "audio.playback", "play start backend=malgo bytes=%d", len(pcm))

	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = DefaultChannels
	config.SampleRate = DefaultSampleRate

	var (
		offset int
		once   sync.Once
		done   = make(chan struct{})
	)

	deviceCallbacks := malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			if len(output) == 0 {
				return
			}
			if s.stopPlayback.Load() {
				for i := range output {
					output[i] = 0
				}
				once.Do(func() { close(done) })
				return
			}
			written := copy(output, pcm[offset:])
			offset += written
			if written < len(output) {
				for i := written; i < len(output); i++ {
					output[i] = 0
				}
			}
			if written > 0 && onLevel != nil {
				onLevel(NormalizedRMS(output[:written]))
			}
			if offset >= len(pcm) {
				once.Do(func() { close(done) })
			}
		},
		Stop: func() {
			once.Do(func() { close(done) })
		},
	}

	device, err := malgo.InitDevice(s.context.Context, config, deviceCallbacks)
	if err != nil {
		return fmt.Errorf("init playback device: %w", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return fmt.Errorf("start playback device: %w", err)
	}

	<-done
	if !s.stopPlayback.Load() {
		time.Sleep(120 * time.Millisecond)
	}
	_ = device.Stop()
	if onLevel != nil {
		onLevel(0)
	}
	s.logger.SessionInfof(sessionID, "audio.playback", "play done backend=malgo bytes=%d", len(pcm))
	return nil
}

func (s *Service) StopPlayback() {
	s.stopPlayback.Store(true)
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.playbackCmd != nil && s.playbackCmd.Process != nil {
		_ = s.playbackCmd.Process.Kill()
	}
}

func (s *Service) playWithSystemPlayer(sessionID string, pcm []byte, onLevel func(float64)) error {
	player, args, err := playbackCommand()
	if err != nil {
		return err
	}

	wavData := pcmToWAV(pcm, DefaultSampleRate, DefaultChannels, 16)
	file, err := os.CreateTemp("", "miko-tts-*.wav")
	if err != nil {
		return fmt.Errorf("create temp wav: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if _, err := file.Write(wavData); err != nil {
		file.Close()
		return fmt.Errorf("write temp wav: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp wav: %w", err)
	}

	cmd := exec.Command(player, append(args, tempPath)...)
	s.logger.SessionInfof(sessionID, "audio.playback", "play start backend=system player=%s bytes=%d", filepath.Base(player), len(pcm))

	stopLevels := make(chan struct{})
	defer close(stopLevels)

	if onLevel != nil {
		go streamLevels(pcm, stopLevels, onLevel)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start system audio player: %w", err)
	}

	s.commandMu.Lock()
	s.playbackCmd = cmd
	s.commandMu.Unlock()

	waitErr := cmd.Wait()

	s.commandMu.Lock()
	if s.playbackCmd == cmd {
		s.playbackCmd = nil
	}
	s.commandMu.Unlock()

	if s.stopPlayback.Load() {
		s.logger.SessionInfof(sessionID, "audio.playback", "play stopped backend=system")
		return nil
	}
	if waitErr != nil {
		return fmt.Errorf("system audio player failed: %w", waitErr)
	}
	if onLevel != nil {
		onLevel(0)
	}
	s.logger.SessionInfof(sessionID, "audio.playback", "play done backend=system player=%s bytes=%d", filepath.Base(player), len(pcm))
	return nil
}

func playbackCommand() (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("afplay"); err == nil {
			return path, nil, nil
		}
	case "linux":
		if path, err := exec.LookPath("aplay"); err == nil {
			return path, []string{"-q"}, nil
		}
		if path, err := exec.LookPath("paplay"); err == nil {
			return path, nil, nil
		}
		if path, err := exec.LookPath("ffplay"); err == nil {
			return path, []string{"-nodisp", "-autoexit", "-loglevel", "error"}, nil
		}
	}
	return "", nil, errNoSystemPlaybackCommand
}

func pcmToWAV(pcm []byte, sampleRate int, channels int, bitsPerSample int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm)

	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	return append(header, pcm...)
}

func streamLevels(pcm []byte, stop <-chan struct{}, onLevel func(float64)) {
	const frameBytes = 640 // 20ms of 16kHz mono int16 PCM
	for offset := 0; offset < len(pcm); offset += frameBytes {
		end := offset + frameBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		onLevel(NormalizedRMS(pcm[offset:end]))
		select {
		case <-stop:
			onLevel(0)
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	onLevel(0)
}

func NormalizedRMS(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}

	var total float64
	var count int
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		normalized := float64(sample) / 32768.0
		total += normalized * normalized
		count++
	}
	if count == 0 {
		return 0
	}
	rms := math.Sqrt(total / float64(count))
	return clamp(rms*4.2, 0, 1)
}

func SpeechActive(pcm []byte, threshold float64) bool {
	return NormalizedRMS(pcm) >= threshold
}

func clamp(value float64, minimum float64, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
