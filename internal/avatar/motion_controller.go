package avatar

import "sync"

type MotionController struct {
	mu          sync.Mutex
	sequence    int64
	pose        *PoseManager
	expressions *ExpressionManager
}

func NewMotionController() *MotionController {
	return &MotionController{
		pose:        NewPoseManager(),
		expressions: NewExpressionManager(),
	}
}

func (m *MotionController) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sequence = 0
	m.pose.Reset()
	m.expressions.Reset()
}

func (m *MotionController) SetAudioLevel(value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expressions.SetMouthOpen(value)
}

func (m *MotionController) SetFaceTarget(x float64, y float64, present bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pose.SetFaceTarget(x, y, present)
}

func (m *MotionController) Tick(delta float64) AvatarMotionFrame {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sequence++
	return AvatarMotionFrame{
		Version:     1,
		Sequence:    m.sequence,
		Pose:        m.pose.Tick(delta),
		Expressions: m.expressions.Tick(delta),
	}
}
