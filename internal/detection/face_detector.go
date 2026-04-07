package detection

import (
	"image"
	"math"

	"gocv.io/x/gocv"
)

// smoothAlpha controls EMA smoothing: closer to 1.0 = more responsive, closer to 0.0 = smoother.
const smoothAlpha = 0.35

// minMotionSize is the minimum width/height (px) for a motion region to be considered.
const minMotionSize = 10

// minIoU is the minimum Intersection-over-Union to match a new detection to a tracked face.
const minIoU = 0.15

// ── Rolling-average filter constants ────────────────────────────────────────

// areaHistoryLen is the number of past frames used to compute the rolling
// average face area. Larger values give a more stable baseline but react
// more slowly to genuine size changes (e.g. person walking closer).
const areaHistoryLen = 15

// minHistoryForFilter is the minimum number of frames that must be in the
// history buffer before area- or jump-based filtering kicks in.
// During early frames the filter is intentionally skipped so the tracker
// can establish a baseline quickly.
const minHistoryForFilter = 5

// maxAreaRatio is the maximum allowed ratio between a candidate's area and
// the rolling average. A detection is rejected when:
//
//	candidate_area > avg * maxAreaRatio  (sudden enlargement)
//	candidate_area < avg / maxAreaRatio  (sudden shrinkage)
const maxAreaRatio = 2.5

// maxCenterJumpFrac is the maximum allowed displacement of the face centre
// expressed as a fraction of the frame diagonal. Detections whose centre
// jumps farther than this in a single frame are considered spurious.
const maxCenterJumpFrac = 0.20

// absentTimeoutFrames is the number of consecutive frames with no valid face
// detection before the detector declares that nobody is in front of the camera.
// At 30 fps, 60 frames ≈ 2 seconds. Increase for a longer grace period.
const absentTimeoutFrames = 60

// ─────────────────────────────────────────────
// TrackedFace – a single tracked face instance
// ─────────────────────────────────────────────

// fRect is a float64 version of image.Rectangle for smooth interpolation.
type fRect struct{ x, y, x2, y2 float64 }

func toFRect(r image.Rectangle) fRect {
	return fRect{
		x:  float64(r.Min.X),
		y:  float64(r.Min.Y),
		x2: float64(r.Max.X),
		y2: float64(r.Max.Y),
	}
}

func (f fRect) toRect() image.Rectangle {
	return image.Rect(int(math.Round(f.x)), int(math.Round(f.y)),
		int(math.Round(f.x2)), int(math.Round(f.y2)))
}

// lerp applies EMA to each coordinate.
func (f fRect) lerp(target image.Rectangle, alpha float64) fRect {
	t := toFRect(target)
	return fRect{
		x:  f.x + alpha*(t.x-f.x),
		y:  f.y + alpha*(t.y-f.y),
		x2: f.x2 + alpha*(t.x2-f.x2),
		y2: f.y2 + alpha*(t.y2-f.y2),
	}
}

type trackedFace struct {
	smooth  fRect // smoothed (display) rect
	matched bool  // was it matched this frame?
}

// ─────────────────────────────────────────────
// FaceDetector
// ─────────────────────────────────────────────

// FaceDetector detects and tracks a single primary face across frames.
// It uses frame-differencing to narrow the search region, a Haar-cascade
// classifier for detection, IoU matching for continuity, and EMA smoothing
// to avoid jitter.
type FaceDetector struct {
	classifier gocv.CascadeClassifier

	// motion-diff mats
	prevGray gocv.Mat
	gray     gocv.Mat
	diffMat  gocv.Mat
	thresh   gocv.Mat

	// tracked state
	track *trackedFace

	// frame dimensions (updated each Detect call)
	imgW, imgH int

	// ── Rolling-average area filter ──────────────────────────────────────
	// areaHistory is a circular buffer storing rectArea of each accepted
	// face detection over the last areaHistoryLen frames.
	areaHistory [areaHistoryLen]int
	areaIdx     int // next write position in the circular buffer
	areaCount   int // how many valid entries are currently in the buffer

	// ── Absence detection ────────────────────────────────────────────────
	// missFrames counts consecutive frames in which no valid face was found.
	// Once it reaches absentTimeoutFrames the tracker is reset and
	// facePresent is set to false.
	missFrames  int
	facePresent bool // true while a face is considered to be in the frame
}

// NewFaceDetector creates a FaceDetector and loads the Haar cascade from cascadePath.
// Returns an error string (non-empty) if the file cannot be loaded.
func NewFaceDetector(cascadePath string) (*FaceDetector, bool) {
	fd := &FaceDetector{
		classifier: gocv.NewCascadeClassifier(),
		prevGray:   gocv.NewMat(),
		gray:       gocv.NewMat(),
		diffMat:    gocv.NewMat(),
		thresh:     gocv.NewMat(),
	}
	if !fd.classifier.Load(cascadePath) {
		fd.Close()
		return nil, false
	}
	return fd, true
}

// Close releases all OpenCV resources held by FaceDetector.
func (fd *FaceDetector) Close() {
	fd.classifier.Close()
	fd.prevGray.Close()
	fd.gray.Close()
	fd.diffMat.Close()
	fd.thresh.Close()
}

// Detect analyses img and returns the smoothed bounding rectangle of the
// primary (largest / closest) face, plus a boolean indicating whether a
// face is currently being tracked.
//
// After absentTimeoutFrames consecutive frames with no valid detection the
// tracker resets and IsPresent() returns false.
func (fd *FaceDetector) Detect(img gocv.Mat) (image.Rectangle, bool) {
	// store frame size for NormalizedPosition
	fd.imgW = img.Cols()
	fd.imgH = img.Rows()

	// ── 1. Compute motion mask ──────────────────────────────────────────
	gocv.CvtColor(img, &fd.gray, gocv.ColorBGRToGray)

	if fd.prevGray.Empty() {
		fd.gray.CopyTo(&fd.prevGray)
		return image.Rectangle{}, false
	}

	gocv.AbsDiff(fd.gray, fd.prevGray, &fd.diffMat)
	gocv.Threshold(fd.diffMat, &fd.thresh, 25, 255, gocv.ThresholdBinary)

	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Pt(15, 15))
	gocv.Dilate(fd.thresh, &fd.thresh, kernel)
	kernel.Close()

	fd.gray.CopyTo(&fd.prevGray)

	motionRect := motionBoundingRect(fd.thresh)
	hasMotion := motionRect.Dx() > minMotionSize && motionRect.Dy() > minMotionSize

	// ── 2. Run classifier in motion ROI ────────────────────────────────
	var rawDetections []image.Rectangle

	if hasMotion {
		// Clamp ROI to image bounds
		imgBounds := image.Rect(0, 0, img.Cols(), img.Rows())
		roi2 := motionRect.Intersect(imgBounds)

		if !roi2.Empty() {
			roiMat := img.Region(roi2)
			detected := fd.classifier.DetectMultiScale(roiMat)
			roiMat.Close()

			// Translate back to full-image coordinates
			for _, f := range detected {
				rawDetections = append(rawDetections, image.Rect(
					roi2.Min.X+f.Min.X, roi2.Min.Y+f.Min.Y,
					roi2.Min.X+f.Max.X, roi2.Min.Y+f.Max.Y,
				))
			}
		}
	} else if fd.track != nil {
		// No motion – keep last smoothed position but still tick the miss
		// counter so a perfectly still person who walked away is eventually
		// timed out (motion simply drops to zero once they're gone).
		// We do NOT increment missFrames here because a static scene is
		// indistinguishable from a still face; only a full absence of a
		// detection inside a motion region counts as a miss.
		return fd.track.smooth.toRect(), true
	}

	// ── 3. Pick largest detection (closest to camera) ──────────────────
	best := largestRect(rawDetections)

	// ── 4. No detection or failed plausibility ──────────────────────────
	// Handle the "no face found" path first so absence tracking is in one place.
	if best.Empty() || !fd.isPlausibleDetection(best) {
		// Increment miss counter.
		fd.missFrames++

		if fd.missFrames >= absentTimeoutFrames {
			// Timeout reached – declare nobody present and wipe all state
			// so the detector starts fresh when a face reappears.
			fd.track = nil
			fd.facePresent = false
			fd.areaCount = 0
			fd.areaIdx = 0
			return image.Rectangle{}, false
		}

		// Within grace period – keep the last known position if available.
		if fd.track != nil {
			return fd.track.smooth.toRect(), true
		}
		return image.Rectangle{}, false
	}

	// ── 5. Valid detection – update tracker ─────────────────────────────
	// Reset miss counter and mark face as present.
	fd.missFrames = 0
	fd.facePresent = true

	if fd.track == nil {
		// First plausible detection – initialise tracker.
		fd.track = &trackedFace{smooth: toFRect(best)}
	} else {
		// IoU matching + EMA smoothing.
		iou := iouRect(fd.track.smooth.toRect(), best)
		if iou >= minIoU {
			fd.track.smooth = fd.track.smooth.lerp(best, smoothAlpha)
		} else {
			fd.track.smooth = toFRect(best)
		}
	}

	// Record accepted area into the rolling history.
	fd.pushArea(rectArea(best))

	return fd.track.smooth.toRect(), true
}

// MotionBoundingRect is exported for callers that want to draw the motion region.
func (fd *FaceDetector) MotionBoundingRect(img gocv.Mat) image.Rectangle {
	return motionBoundingRect(fd.thresh)
}

// NormalizedPosition returns the centre of the tracked face as (x, y) values
// in the range [0, 1], where:
//
//	(0, 0) = top-left corner of the frame
//	(1, 1) = bottom-right corner of the frame
//
// ok is false when no face is currently tracked or the frame size is unknown.
func (fd *FaceDetector) NormalizedPosition() (x, y float64, ok bool) {
	if fd.track == nil || fd.imgW == 0 || fd.imgH == 0 {
		return 0, 0, false
	}
	r := fd.track.smooth
	cx := (r.x + r.x2) / 2.0
	cy := (r.y + r.y2) / 2.0
	x = math.Max(0, math.Min(1, cx/float64(fd.imgW)))
	y = math.Max(0, math.Min(1, cy/float64(fd.imgH)))
	return x, y, true
}

// IsPresent returns true if a face has been detected and the absence timeout
// has not yet elapsed. This is the primary signal for "is someone in front of
// the camera right now?"
func (fd *FaceDetector) IsPresent() bool {
	return fd.facePresent
}

// MissFrames returns the number of consecutive frames in which no valid face
// detection was found. Resets to 0 on every accepted detection.
// Callers can use this to display a countdown or drive application logic
// before the full absentTimeoutFrames threshold is reached.
func (fd *FaceDetector) MissFrames() int {
	return fd.missFrames
}

// ─────────────────────────────────────────────
// Rolling-average area filter
// ─────────────────────────────────────────────

// pushArea records area into the circular history buffer.
func (fd *FaceDetector) pushArea(area int) {
	fd.areaHistory[fd.areaIdx] = area
	fd.areaIdx = (fd.areaIdx + 1) % areaHistoryLen
	if fd.areaCount < areaHistoryLen {
		fd.areaCount++
	}
}

// RollingAvgArea returns the rolling average face area (px²) over the last
// areaHistoryLen accepted detections, and the number of samples used.
// If no samples are available yet, returns (0, 0).
func (fd *FaceDetector) RollingAvgArea() (avg float64, n int) {
	n = fd.areaCount
	if n == 0 {
		return 0, 0
	}
	sum := 0
	for i := 0; i < n; i++ {
		sum += fd.areaHistory[i]
	}
	return float64(sum) / float64(n), n
}

// isPlausibleDetection returns true when candidate passes the area-ratio and
// centre-jump plausibility checks against the rolling history.
//
// It returns true unconditionally until minHistoryForFilter accepted frames
// have been accumulated, so the tracker can bootstrap without being blocked.
func (fd *FaceDetector) isPlausibleDetection(candidate image.Rectangle) bool {
	if fd.areaCount < minHistoryForFilter {
		// Not enough history yet – let anything through.
		return true
	}

	avgArea, _ := fd.RollingAvgArea()
	candArea := float64(rectArea(candidate))

	// ── Area-ratio check ────────────────────────────────────────────────
	// Reject if the candidate is dramatically larger or smaller than the
	// rolling average (both directions use the same ratio threshold).
	if candArea > avgArea*maxAreaRatio {
		return false // suddenly much larger → likely false positive
	}
	if candArea < avgArea/maxAreaRatio {
		return false // suddenly much smaller → likely false positive
	}

	// ── Centre-jump check ───────────────────────────────────────────────
	// Reject if the face centre has moved further than maxCenterJumpFrac
	// of the frame diagonal in a single frame.
	if fd.track != nil && fd.imgW > 0 && fd.imgH > 0 {
		diag := math.Sqrt(float64(fd.imgW*fd.imgW + fd.imgH*fd.imgH))
		maxJump := maxCenterJumpFrac * diag

		// Current smoothed centre
		sr := fd.track.smooth
		scx := (sr.x + sr.x2) / 2.0
		scy := (sr.y + sr.y2) / 2.0

		// Candidate centre
		ccx := float64(candidate.Min.X+candidate.Max.X) / 2.0
		ccy := float64(candidate.Min.Y+candidate.Max.Y) / 2.0

		dx := ccx - scx
		dy := ccy - scy
		jump := math.Sqrt(dx*dx + dy*dy)

		if jump > maxJump {
			return false // centre teleported → spurious detection
		}
	}

	return true
}

// ─────────────────────────────────────────────
// Helpers (package-level, shared)
// ─────────────────────────────────────────────

// motionBoundingRect returns the union of all contour bounding rects in mask.
func motionBoundingRect(mask gocv.Mat) image.Rectangle {
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return image.Rectangle{}
	}

	union := image.Rectangle{}
	for i := 0; i < contours.Size(); i++ {
		br := gocv.BoundingRect(contours.At(i))
		if union.Empty() {
			union = br
		} else {
			union = union.Union(br)
		}
	}
	return union
}

// largestRect returns the rectangle with the largest area from a slice.
func largestRect(rects []image.Rectangle) image.Rectangle {
	var best image.Rectangle
	for _, r := range rects {
		if rectArea(r) > rectArea(best) {
			best = r
		}
	}
	return best
}

// rectArea returns the pixel area of a rectangle.
func rectArea(r image.Rectangle) int {
	return r.Dx() * r.Dy()
}

// iouRect computes Intersection-over-Union for two rectangles.
func iouRect(a, b image.Rectangle) float64 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	interArea := float64(rectArea(inter))
	unionArea := float64(rectArea(a)+rectArea(b)) - interArea
	if unionArea == 0 {
		return 0
	}
	return interArea / unionArea
}
