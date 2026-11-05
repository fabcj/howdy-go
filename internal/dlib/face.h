// C interface to the dlib face pipeline, so Go doesn't touch C++ directly
#ifndef HOWDY_GO_FACE_H
#define HOWDY_GO_FACE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct hd_face hd_face;

enum { HD_ERRLEN = 256 };

// Every call returns 0 or more on success and -1 on failure, with a message
// in err (HD_ERRLEN bytes)
hd_face *hd_new(void);
void hd_free(hd_face *f);

int hd_load_hog(hd_face *f, const char *path, char *err);
// dlib's built in HOG face detector, about 300 ms slower to set up
int hd_use_builtin_hog(hd_face *f, char *err);
int hd_load_cnn(hd_face *f, const char *path, char *err);
int hd_load_shape(hd_face *f, const char *path, char *err);
int hd_load_encoder(hd_face *f, const char *path, char *err);

// Finds faces in a grey image. rects gets left, top, right, bottom for up to
// max faces. Returns how many were found, which may be more than max.
int hd_detect(hd_face *f, const uint8_t *grey, int w, int h, int upsample,
              int use_cnn, long *rects, int max, char *err);

// Landmarks and the 128 number descriptor for one face box, from the grey
// image used as all three channels of an RGB one
int hd_encode(hd_face *f, const uint8_t *grey, int w, int h, const long *rect,
              double *descriptor, long *parts, char *err);

// Writes dlib's built in HOG face detector to path
int hd_save_frontal(const char *path, char *err);

// Pins OpenBLAS to one thread if it's loaded, returns 1 if it was
int hd_blas_single_thread(void);

#ifdef __cplusplus
}
#endif

#endif
