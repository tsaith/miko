package avatar

import (
	"math/rand"
)

const (
	blinkCloseDuration = 0.06
	blinkOpenDuration  = 0.14
)

type ExpressionManager struct {
	targetMouth    float64
	currentMouth   float64
	blinkTimer     float64
	blinkPhase     string
	blinkPhaseTime float64
	blinkValue     float64
}

func NewExpressionManager() *ExpressionManager {
	manager := &ExpressionManager{}
	manager.Reset()
	return manager
}

func (m *ExpressionManager) Reset() {
	m.targetMouth = 0
	m.currentMouth = 0
	m.blinkTimer = randomBlinkInterval()
	m.blinkPhase = "idle"
	m.blinkPhaseTime = 0
	m.blinkValue = 0
}

func (m *ExpressionManager) SetMouthOpen(value float64) {
	m.targetMouth = clamp(value, 0, 1)
}

func (m *ExpressionManager) Tick(delta float64) AvatarExpressionFrame {
	m.tickLipSync()
	m.tickBlink(delta)
	return AvatarExpressionFrame{
		AA:    rounded(m.currentMouth),
		Blink: rounded(m.blinkValue),
	}
}

func (m *ExpressionManager) tickLipSync() {
	m.currentMouth += (m.targetMouth - m.currentMouth) * 0.3
}

func (m *ExpressionManager) tickBlink(delta float64) {
	if m.blinkPhase == "idle" {
		m.blinkTimer -= delta
		m.blinkValue = 0
		if m.blinkTimer <= 0 {
			m.blinkPhase = "closing"
			m.blinkPhaseTime = 0
		}
		return
	}

	m.blinkPhaseTime += delta
	if m.blinkPhase == "closing" {
		t := clamp(m.blinkPhaseTime/blinkCloseDuration, 0, 1)
		m.blinkValue = t * t
		if t >= 1 {
			m.blinkPhase = "opening"
			m.blinkPhaseTime = 0
		}
		return
	}

	t := clamp(m.blinkPhaseTime/blinkOpenDuration, 0, 1)
	m.blinkValue = (1 - t) * (1 - t)
	if t >= 1 {
		m.blinkPhase = "idle"
		m.blinkPhaseTime = 0
		m.blinkValue = 0
		m.blinkTimer = randomBlinkInterval()
	}
}

func randomBlinkInterval() float64 {
	return 3 + rand.Float64()*5
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
