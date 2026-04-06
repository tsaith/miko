package avatar

import (
	"math"
	"math/rand"
)

const (
	aPoseUpperArm = math.Pi * 0.3

	inhaleDuration = 6.0
	exhaleDuration = 4.0
	breathShoulder = 0.06
	breathChestZ   = 0.03

	eyeHoldMin = 1.5
	eyeHoldMax = 3.5
	eyeMaxYaw  = 0.054
	eyeLerp    = 0.08

	shiftHoldMin = 4.0
	shiftHoldMax = 8.0
	shiftHipZ    = 0.015
	shiftSpineZ  = 0.007
	shiftLerp    = 0.008

	windGravityX  = -0.6
	windGravityZ  = -0.2
	windHeadTiltZ = 0.05
	windHeadTurnY = -0.04
	windNeckTiltZ = 0.03
	windCalmMin   = 10.0
	windCalmMax   = 22.0
	windBuildTime = 1.5
	windDieTime   = 2.0
	windPeakMin   = 2.0
	windPeakMax   = 4.5
)

type poseState struct {
	breathTime    float64
	eyeTimer      float64
	eyeTargetY    float64
	eyeCurrentY   float64
	shiftTimer    float64
	shiftTargetZ  float64
	shiftCurrentZ float64
	windPhase     string
	windTimer     float64
	windIntensity float64
}

type PoseManager struct {
	state poseState
}

func NewPoseManager() *PoseManager {
	manager := &PoseManager{}
	manager.Reset()
	return manager
}

func (m *PoseManager) Reset() {
	m.state = poseState{
		eyeTimer:   randomIn(eyeHoldMin, eyeHoldMax),
		shiftTimer: randomIn(shiftHoldMin, shiftHoldMax),
		windPhase:  "calm",
		windTimer:  randomIn(windCalmMin, windCalmMax),
	}
}

func (m *PoseManager) Tick(delta float64) AvatarPoseFrame {
	m.state.breathTime += delta

	breathValue := m.breathValue()
	eyeY := m.tickEyes(delta)
	hipsZ, spineZ := m.tickWeightShift(delta)
	wind := m.tickWind(delta)

	return AvatarPoseFrame{
		Base: map[string]float64{
			"leftUpperArmZ":  rounded(-aPoseUpperArm),
			"rightUpperArmZ": rounded(aPoseUpperArm),
		},
		Breathing: map[string]float64{
			"value":     rounded(breathValue),
			"shoulderZ": rounded(breathValue * breathShoulder),
			"chestZ":    rounded(breathValue * breathChestZ),
		},
		Gaze: map[string]float64{
			"eyeY": rounded(eyeY),
		},
		WeightShift: map[string]float64{
			"hipsZ":  rounded(hipsZ),
			"spineZ": rounded(spineZ),
		},
		Wind: wind,
	}
}

func (m *PoseManager) breathValue() float64 {
	cycle := inhaleDuration + exhaleDuration
	phase := math.Mod(m.state.breathTime, cycle)
	if phase < inhaleDuration {
		t := phase / inhaleDuration
		return (1 - math.Cos(t*math.Pi)) / 2
	}
	t := (phase - inhaleDuration) / exhaleDuration
	return (1 + math.Cos(t*math.Pi)) / 2
}

func (m *PoseManager) tickEyes(delta float64) float64 {
	m.state.eyeTimer -= delta
	if m.state.eyeTimer <= 0 {
		if rand.Float64() < 0.25 {
			m.state.eyeTargetY = 0
		} else {
			m.state.eyeTargetY = randomIn(-eyeMaxYaw, eyeMaxYaw)
		}
		m.state.eyeTimer = randomIn(eyeHoldMin, eyeHoldMax)
	}
	m.state.eyeCurrentY += (m.state.eyeTargetY - m.state.eyeCurrentY) * eyeLerp
	return m.state.eyeCurrentY
}

func (m *PoseManager) tickWeightShift(delta float64) (float64, float64) {
	m.state.shiftTimer -= delta
	if m.state.shiftTimer <= 0 {
		roll := rand.Float64()
		switch {
		case roll < 0.33:
			m.state.shiftTargetZ = -shiftHipZ
		case roll < 0.5:
			m.state.shiftTargetZ = 0
		default:
			m.state.shiftTargetZ = shiftHipZ
		}
		m.state.shiftTimer = randomIn(shiftHoldMin, shiftHoldMax)
	}
	m.state.shiftCurrentZ += (m.state.shiftTargetZ - m.state.shiftCurrentZ) * shiftLerp
	spineZ := -m.state.shiftCurrentZ * (shiftSpineZ / shiftHipZ)
	return m.state.shiftCurrentZ, spineZ
}

func (m *PoseManager) tickWind(delta float64) AvatarWindFrame {
	m.state.windTimer -= delta

	switch m.state.windPhase {
	case "calm":
		m.state.windIntensity = 0
		if m.state.windTimer <= 0 {
			m.state.windPhase = "building"
			m.state.windTimer = windBuildTime
		}
	case "building":
		elapsed := windBuildTime - math.Max(0, m.state.windTimer)
		m.state.windIntensity = elapsed / windBuildTime
		if m.state.windTimer <= 0 {
			m.state.windIntensity = 1
			m.state.windPhase = "peak"
			m.state.windTimer = randomIn(windPeakMin, windPeakMax)
		}
	case "peak":
		m.state.windIntensity = 1
		if m.state.windTimer <= 0 {
			m.state.windPhase = "dying"
			m.state.windTimer = windDieTime
		}
	case "dying":
		m.state.windIntensity = math.Max(0, m.state.windTimer) / windDieTime
		if m.state.windTimer <= 0 {
			m.state.windIntensity = 0
			m.state.windPhase = "calm"
			m.state.windTimer = randomIn(windCalmMin, windCalmMax)
		}
	}

	intensity := m.state.windIntensity
	return AvatarWindFrame{
		Intensity: rounded(intensity),
		Gravity: Vector3Frame{
			X: rounded(windGravityX * intensity),
			Y: 0,
			Z: rounded(windGravityZ * intensity),
		},
		Head: Rotation3Frame{
			X: 0,
			Y: rounded(windHeadTurnY * intensity),
			Z: rounded(windHeadTiltZ * intensity),
		},
		Neck: Rotation3Frame{
			X: 0,
			Y: 0,
			Z: rounded(windNeckTiltZ * intensity),
		},
	}
}

func randomIn(minimum float64, maximum float64) float64 {
	return minimum + rand.Float64()*(maximum-minimum)
}
