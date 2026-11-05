package sface

// ArcFace's 112x112 template, which FaceRecognizerSF::alignCrop maps the
// five YuNet points onto
var template = [5][2]float64{
	{38.2946, 51.6963}, {73.5318, 51.5014}, {56.0252, 71.7366},
	{41.5493, 92.3655}, {70.7299, 92.2041},
}

// OpenCV hard-codes the template's mean as float
var templateMean = [2]float64{float64(float32(56.0262)), float64(float32(71.9008))}

// similarity is getSimilarityTransformMatrix from face_recognize.cpp: the
// least squares rotation, scale and shift taking src onto the template
// (Umeyama's method). For two dimensions the rotation has a closed form, so
// there's no SVD. The demeaned points are float32 as in OpenCV.
func similarity(src [5][2]float32) [6]float64 {
	var mean [2]float32
	for _, p := range src {
		mean[0] += p[0]
		mean[1] += p[1]
	}
	mean[0] /= 5
	mean[1] /= 5

	// a = sum(dst . src), b = sum(dst x src), over the source variance
	var a, b, v float64
	for i, p := range src {
		sx := float64(p[0] - mean[0])
		sy := float64(p[1] - mean[1])
		dx := float64(float32(template[i][0]) - float32(templateMean[0]))
		dy := float64(float32(template[i][1]) - float32(templateMean[1]))
		a += dx*sx + dy*sy
		b += dy*sx - dx*sy
		v += sx*sx + sy*sy
	}
	a /= v
	b /= v
	mx, my := float64(mean[0]), float64(mean[1])
	return [6]float64{
		a, -b, templateMean[0] - (a*mx - b*my),
		b, a, templateMean[1] - (b*mx + a*my),
	}
}
