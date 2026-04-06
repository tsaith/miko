package avatar

import "math"

type Vector3Frame struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type Rotation3Frame = Vector3Frame

type AvatarWindFrame struct {
	Intensity float64      `json:"intensity"`
	Gravity   Vector3Frame `json:"gravity"`
	Head      Vector3Frame `json:"head"`
	Neck      Vector3Frame `json:"neck"`
}

type AvatarPoseFrame struct {
	Base        map[string]float64 `json:"base"`
	Breathing   map[string]float64 `json:"breathing"`
	Gaze        map[string]float64 `json:"gaze"`
	WeightShift map[string]float64 `json:"weightShift"`
	Wind        AvatarWindFrame    `json:"wind"`
}

type AvatarExpressionFrame struct {
	AA    float64 `json:"aa"`
	Blink float64 `json:"blink"`
}

type AvatarMotionFrame struct {
	Version     int                   `json:"version"`
	Sequence    int64                 `json:"sequence"`
	Pose        AvatarPoseFrame       `json:"pose"`
	Expressions AvatarExpressionFrame `json:"expressions"`
}

func rounded(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}
