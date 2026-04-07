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

	eyeHoldMin   = 1.5
	eyeHoldMax   = 3.5
	eyeMaxYaw    = 0.038
	eyeMaxPitch  = 0.038
	eyeLerp      = 0.08
	faceEyeLerp  = 0.18
	headMaxYaw   = 0.11
	headMaxPitch = 0.11
	neckMaxYaw   = 0.05
	neckMaxPitch = 0.05
	headLerp     = 0.14
	neckLerp     = 0.12

	shiftHoldMin = 4.0
	shiftHoldMax = 8.0
	shiftHipZ    = 0.015
	shiftSpineZ  = 0.007
	shiftLerp    = 0.008

	windBaseDirX   = -0.82
	windBaseDirY   = -0.05
	windBaseDirZ   = -0.56
	windHeadTiltZ  = 0.045
	windHeadTurnY  = -0.035
	windNeckTiltZ  = 0.025
	windShiftMin   = 1.4
	windShiftMax   = 3.4
	windBaseMin    = 0.2
	windBaseMax    = 0.42
	windGustMin    = 0.58
	windGustMax    = 0.82
	windGustChance = 0.22
	windResponse   = 2.3
)

type poseState struct {
	breathTime    float64
	eyeTimer      float64
	eyeTargetY    float64
	eyeCurrentX   float64
	eyeCurrentY   float64
	headCurrentX  float64
	headCurrentY  float64
	neckCurrentX  float64
	neckCurrentY  float64
	faceTargetX   float64
	faceTargetY   float64
	faceNormX     float64
	faceNormY     float64
	facePresent   bool
	shiftTimer    float64
	shiftTargetZ  float64
	shiftCurrentZ float64
	windTimer     float64
	windTime      float64
	windCurrent   float64
	windTarget    float64
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
		eyeTimer:    randomIn(eyeHoldMin, eyeHoldMax),
		shiftTimer:  randomIn(shiftHoldMin, shiftHoldMax),
		windTimer:   randomIn(windShiftMin, windShiftMax),
		windTarget:  randomIn(windBaseMin, windBaseMax),
		windCurrent: randomIn(windBaseMin*0.7, windBaseMax*0.9),
	}
}

func (m *PoseManager) SetFaceTarget(x float64, y float64, present bool) {
	m.state.facePresent = present
	if !present {
		return
	}
	m.state.faceNormX = clampFloat(x, 0, 1)
	m.state.faceNormY = clampFloat(y, 0, 1)
	m.state.faceTargetX = clampFloat((y-0.5)*2.0*eyeMaxPitch, -eyeMaxPitch, eyeMaxPitch)
	// Use the camera's physical coordinate system directly. The webcam is acting
	// as the assistant's eyes, so we invert the horizontal gaze sign to match the
	// VRM eye-bone yaw convention instead of mirroring the detection image.
	m.state.faceTargetY = clampFloat((0.5-x)*2.0*eyeMaxYaw, -eyeMaxYaw, eyeMaxYaw)
}

func (m *PoseManager) Tick(delta float64) AvatarPoseFrame {
	m.state.breathTime += delta

	breathValue := m.breathValue()
	eyeX, eyeY := m.tickEyes(delta)
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
			"eyeX": rounded(eyeX),
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

func (m *PoseManager) tickEyes(delta float64) (float64, float64) {
	if m.state.facePresent {
		m.state.eyeCurrentX += (m.state.faceTargetX - m.state.eyeCurrentX) * faceEyeLerp
		m.state.eyeCurrentY += (m.state.faceTargetY - m.state.eyeCurrentY) * faceEyeLerp
		return m.state.eyeCurrentX, m.state.eyeCurrentY
	}

	m.state.eyeTimer -= delta
	if m.state.eyeTimer <= 0 {
		if rand.Float64() < 0.25 {
			m.state.eyeTargetY = 0
		} else {
			m.state.eyeTargetY = randomIn(-eyeMaxYaw, eyeMaxYaw)
		}
		m.state.eyeTimer = randomIn(eyeHoldMin, eyeHoldMax)
	}
	m.state.eyeCurrentX += (0 - m.state.eyeCurrentX) * eyeLerp
	m.state.eyeCurrentY += (m.state.eyeTargetY - m.state.eyeCurrentY) * eyeLerp
	return m.state.eyeCurrentX, m.state.eyeCurrentY
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
	m.state.windTime += delta
	m.state.windTimer -= delta

	if m.state.windTimer <= 0 {
		if rand.Float64() < windGustChance {
			m.state.windTarget = randomIn(windGustMin, windGustMax)
			m.state.windTimer = randomIn(0.9, 1.8)
		} else {
			m.state.windTarget = randomIn(windBaseMin, windBaseMax)
			m.state.windTimer = randomIn(windShiftMin, windShiftMax)
		}
	}

	smoothing := 1 - math.Exp(-delta*windResponse)
	m.state.windCurrent += (m.state.windTarget - m.state.windCurrent) * smoothing

	t := m.state.windTime
	strengthPulse := 1 +
		0.16*math.Sin(t*0.63+0.4) +
		0.08*math.Sin(t*1.37+1.9) +
		0.04*math.Sin(t*3.8+0.7)
	intensity := clampFloat(m.state.windCurrent*strengthPulse, 0.08, 0.95)

	dirX := windBaseDirX +
		0.08*math.Sin(t*0.74+0.2) +
		0.05*math.Sin(t*2.15+2.1)
	dirY := windBaseDirY +
		0.03*math.Sin(t*1.1+1.6) +
		0.015*math.Sin(t*4.4+0.5)
	dirZ := windBaseDirZ +
		0.07*math.Sin(t*0.58+2.4) +
		0.03*math.Sin(t*1.83+1.1)
	dirLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if dirLen < 1e-6 {
		dirLen = 1
	}
	gravityX := (dirX / dirLen) * intensity
	gravityY := (dirY / dirLen) * intensity
	gravityZ := (dirZ / dirLen) * intensity

	headXTarget, headYTarget, neckXTarget, neckYTarget := m.faceTrackingTargets()
	m.state.headCurrentX += (headXTarget - m.state.headCurrentX) * headLerp
	m.state.headCurrentY += (headYTarget - m.state.headCurrentY) * headLerp
	m.state.neckCurrentX += (neckXTarget - m.state.neckCurrentX) * neckLerp
	m.state.neckCurrentY += (neckYTarget - m.state.neckCurrentY) * neckLerp

	return AvatarWindFrame{
		Intensity: rounded(intensity),
		Gravity: Vector3Frame{
			X: rounded(gravityX),
			Y: rounded(gravityY),
			Z: rounded(gravityZ),
		},
		Head: Rotation3Frame{
			X: rounded(m.state.headCurrentX),
			Y: rounded(m.state.headCurrentY + windHeadTurnY*intensity),
			Z: rounded(windHeadTiltZ * intensity),
		},
		Neck: Rotation3Frame{
			X: rounded(m.state.neckCurrentX),
			Y: rounded(m.state.neckCurrentY),
			Z: rounded(windNeckTiltZ * intensity),
		},
	}
}

func (m *PoseManager) faceTrackingTargets() (float64, float64, float64, float64) {
	if !m.state.facePresent {
		return 0, 0, 0, 0
	}

	vertical := clampFloat((m.state.faceNormY-0.5)*2.0, -1, 1)
	horizontal := clampFloat((0.5-m.state.faceNormX)*2.0, -1, 1)

	headX := clampFloat(vertical*headMaxPitch, -headMaxPitch, headMaxPitch)
	headY := clampFloat(horizontal*headMaxYaw, -headMaxYaw, headMaxYaw)
	neckX := clampFloat(vertical*neckMaxPitch, -neckMaxPitch, neckMaxPitch)
	neckY := clampFloat(horizontal*neckMaxYaw, -neckMaxYaw, neckMaxYaw)

	return headX, headY, neckX, neckY
}

func randomIn(minimum float64, maximum float64) float64 {
	return minimum + rand.Float64()*(maximum-minimum)
}

func clampFloat(value float64, minimum float64, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
