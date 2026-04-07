package detection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gocv.io/x/gocv"

	"miko/internal/debuglog"
)

const cascadeFilename = "haarcascade_frontalface_default.xml"

type Observation struct {
	X       float64
	Y       float64
	Present bool
}

type Service struct {
	logger   *debuglog.Logger
	detector *FaceDetector
	capture  *gocv.VideoCapture
	frame    gocv.Mat
}

func NewService(logger *debuglog.Logger) (*Service, error) {
	cascadePath, err := resolveCascadePath()
	if err != nil {
		return nil, err
	}

	detector, ok := NewFaceDetector(cascadePath)
	if !ok {
		return nil, fmt.Errorf("load face cascade: %s", cascadePath)
	}

	return &Service{
		logger:   logger,
		detector: detector,
		frame:    gocv.NewMat(),
	}, nil
}

func (s *Service) Start(ctx context.Context, onObservation func(Observation)) error {
	capture, err := gocv.OpenVideoCapture(0)
	if err != nil {
		return fmt.Errorf("open default camera: %w", err)
	}
	if !capture.IsOpened() {
		capture.Close()
		return fmt.Errorf("default camera is not available")
	}

	capture.Set(gocv.VideoCaptureFrameWidth, 640)
	capture.Set(gocv.VideoCaptureFrameHeight, 480)
	capture.Set(gocv.VideoCaptureFPS, 30)

	s.capture = capture
	go s.loop(ctx, onObservation)
	return nil
}

func (s *Service) Close() {
	if s.capture != nil {
		_ = s.capture.Close()
		s.capture = nil
	}
	if !s.frame.Empty() {
		s.frame.Close()
		s.frame = gocv.NewMat()
	}
	if s.detector != nil {
		s.detector.Close()
		s.detector = nil
	}
}

func (s *Service) loop(ctx context.Context, onObservation func(Observation)) {
	ticker := time.NewTicker(time.Second / 15)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.capture == nil || s.detector == nil {
				return
			}
			if ok := s.capture.Read(&s.frame); !ok || s.frame.Empty() {
				continue
			}

			_, present := s.detector.Detect(s.frame)
			if !present {
				onObservation(Observation{Present: false})
				continue
			}

			x, y, ok := s.detector.NormalizedPosition()
			if !ok {
				onObservation(Observation{Present: false})
				continue
			}

			onObservation(Observation{
				X:       x,
				Y:       y,
				Present: true,
			})
		}
	}
}

func resolveCascadePath() (string, error) {
	var candidates []string

	cwd, err := os.Getwd()
	if err == nil {
		candidates = append(candidates, filepath.Join(cwd, "models", cascadeFilename))
	}

	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "models", cascadeFilename),
			filepath.Join(exeDir, "..", "models", cascadeFilename),
			filepath.Join(exeDir, "..", "..", "models", cascadeFilename),
			filepath.Join(exeDir, "..", "Resources", "models", cascadeFilename),
			filepath.Join(exeDir, "..", "..", "Resources", "models", cascadeFilename),
		)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Abs(candidate)
		}
	}

	return "", fmt.Errorf("cannot locate models/%s", cascadeFilename)
}
