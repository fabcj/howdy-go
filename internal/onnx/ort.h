// C interface to ONNX Runtime, so Go doesn't deal with its API table
#ifndef HOWDY_GO_ORT_H
#define HOWDY_GO_ORT_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct hd_ort hd_ort;

enum { HD_ORT_ERRLEN = 256 };

// Builds a session from a model already in memory, single threaded. Returns
// NULL with a message in err (HD_ORT_ERRLEN bytes) on failure.
hd_ort *hd_ort_open(const void *model, size_t len, char *err);
void hd_ort_close(hd_ort *s);

// Runs the model on one float tensor of shape dims[ndims] and copies each
// named output into outs[i], which holds caps[i] floats. lens[i] gets how
// many it has. Returns 0, or -1 with a message in err.
int hd_ort_run(hd_ort *s, const int64_t *dims, int ndims, const float *data,
               const char *const *names, int nout, float *const *outs,
               const size_t *caps, size_t *lens, char *err);

// Name of the first output, which stays valid until hd_ort_close
const char *hd_ort_output0(hd_ort *s);

#ifdef __cplusplus
}
#endif

#endif
