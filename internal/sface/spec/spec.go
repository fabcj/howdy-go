// Package spec is what the rest of howdy-go needs to know about the sface
// backend without loading it: its model files and their hashes, how its
// stored descriptors are tagged and how long they are. It has no cgo, so
// backend and the CLI can import it; the loading lives in sface.
package spec

// The model files and what they must hash to (OpenCV Zoo's git-LFS ids)
const (
	YuNetFile   = "face_detection_yunet_2026may.onnx"
	YuNetSHA256 = "ebafce4e3c118d6554634be5c27ab333b4c047a9a8c3faf1d7cf93101c22f0f0"
	SFaceFile   = "face_recognition_sface_2021dec.onnx"
	SFaceSHA256 = "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79"
)

// Encoder tags every stored sface descriptor, so they're never compared
// with another model's
const Encoder = "sface-2021dec"

// Size is how many numbers an sface descriptor has
const Size = 128
