//go:build !noonnx

// The ONNX Runtime calls the YuNet and SFace backend makes, through the C
// API (onnxruntime_c_api.h)

#include "ort.h"

#include <onnxruntime_c_api.h>

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const OrtApi *api;
static OrtEnv *env;
static OrtMemoryInfo *cpu;
static pthread_once_t once = PTHREAD_ONCE_INIT;
static char init_err[HD_ORT_ERRLEN];

struct hd_ort {
  OrtSession *sess;
  char *input;
  char *output0;
};

static void set_err(char *err, const char *msg) {
  if (err == NULL) {
    return;
  }
  snprintf(err, HD_ORT_ERRLEN, "%s", msg);
}

// Takes the status, so every call site can just return through here
static int failed(OrtStatus *st, char *err) {
  if (st == NULL) {
    return 0;
  }
  set_err(err, api->GetErrorMessage(st));
  api->ReleaseStatus(st);
  return 1;
}

// ONNX Runtime's log goes nowhere: stdout is what PAM shows the user
static void quiet(void *param, OrtLoggingLevel severity, const char *category,
                  const char *logid, const char *code_location,
                  const char *message) {
  (void)param, (void)severity, (void)category, (void)logid;
  (void)code_location, (void)message;
}

static void init(void) {
  const OrtApiBase *base = OrtGetApiBase();
  api = base ? base->GetApi(ORT_API_VERSION) : NULL;
  if (api == NULL) {
    snprintf(init_err, sizeof init_err, "ONNX Runtime doesn't support API version %d", ORT_API_VERSION);
    return;
  }
  if (failed(api->CreateEnvWithCustomLogger(quiet, NULL, ORT_LOGGING_LEVEL_FATAL, "howdy", &env), init_err) ||
      failed(api->CreateCpuMemoryInfo(OrtDeviceAllocator, OrtMemTypeDefault, &cpu), init_err)) {
    api = NULL;
  }
}

static char *copy_name(OrtSession *sess, int input, size_t i, char *err) {
  OrtAllocator *alloc;
  char *name = NULL;
  if (failed(api->GetAllocatorWithDefaultOptions(&alloc), err)) {
    return NULL;
  }
  OrtStatus *st = input ? api->SessionGetInputName(sess, i, alloc, &name)
                        : api->SessionGetOutputName(sess, i, alloc, &name);
  if (failed(st, err)) {
    return NULL;
  }
  char *out = strdup(name);
  alloc->Free(alloc, name);
  if (out == NULL) {
    set_err(err, "out of memory");
  }
  return out;
}

hd_ort *hd_ort_open(const void *model, size_t len, char *err) {
  pthread_once(&once, init);
  if (api == NULL) {
    set_err(err, init_err);
    return NULL;
  }

  OrtSessionOptions *opts = NULL;
  hd_ort *s = calloc(1, sizeof *s);
  if (s == NULL) {
    set_err(err, "out of memory");
    return NULL;
  }
  // One thread and no spinning, like BLAS for dlib: compare runs next to
  // whatever the user is doing, and the models are small
  if (failed(api->CreateSessionOptions(&opts), err) ||
      failed(api->SetIntraOpNumThreads(opts, 1), err) ||
      failed(api->SetInterOpNumThreads(opts, 1), err) ||
      failed(api->SetSessionExecutionMode(opts, ORT_SEQUENTIAL), err) ||
      failed(api->SetSessionGraphOptimizationLevel(opts, ORT_ENABLE_ALL), err) ||
      failed(api->AddSessionConfigEntry(opts, "session.intra_op.allow_spinning", "0"), err) ||
      failed(api->AddSessionConfigEntry(opts, "session.inter_op.allow_spinning", "0"), err) ||
      failed(api->CreateSessionFromArray(env, model, len, opts, &s->sess), err)) {
    goto fail;
  }
  api->ReleaseSessionOptions(opts);
  opts = NULL;

  size_t n;
  if (failed(api->SessionGetInputCount(s->sess, &n), err)) {
    goto fail;
  }
  if (n != 1) {
    set_err(err, "model must have exactly one input");
    goto fail;
  }
  if ((s->input = copy_name(s->sess, 1, 0, err)) == NULL ||
      (s->output0 = copy_name(s->sess, 0, 0, err)) == NULL) {
    goto fail;
  }
  return s;

fail:
  if (opts != NULL) {
    api->ReleaseSessionOptions(opts);
  }
  hd_ort_close(s);
  return NULL;
}

void hd_ort_close(hd_ort *s) {
  if (s == NULL) {
    return;
  }
  if (s->sess != NULL) {
    api->ReleaseSession(s->sess);
  }
  free(s->input);
  free(s->output0);
  free(s);
}

const char *hd_ort_output0(hd_ort *s) { return s->output0; }

int hd_ort_run(hd_ort *s, const int64_t *dims, int ndims, const float *data,
               const char *const *names, int nout, float *const *outs,
               const size_t *caps, size_t *lens, char *err) {
  if (nout <= 0 || nout > 16 || ndims <= 0) {
    set_err(err, "bad output or dimension count");
    return -1;
  }
  size_t count = 1;
  for (int i = 0; i < ndims; i++) {
    if (dims[i] <= 0) {
      set_err(err, "bad input shape");
      return -1;
    }
    count *= (size_t)dims[i];
  }

  OrtValue *in = NULL;
  OrtValue *out[16] = {0};
  int rc = -1;
  // ONNX Runtime doesn't write to inputs, the const goes away only here
  if (failed(api->CreateTensorWithDataAsOrtValue(cpu, (void *)data, count * sizeof(float), dims,
                                                 (size_t)ndims, ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &in),
             err)) {
    return -1;
  }
  const char *input = s->input;
  if (failed(api->Run(s->sess, NULL, &input, (const OrtValue *const *)&in, 1, names, (size_t)nout, out), err)) {
    goto done;
  }
  for (int i = 0; i < nout; i++) {
    OrtTensorTypeAndShapeInfo *info;
    ONNXTensorElementDataType type;
    size_t n;
    float *p;
    if (failed(api->GetTensorTypeAndShape(out[i], &info), err)) {
      goto done;
    }
    OrtStatus *st = api->GetTensorElementType(info, &type);
    if (st == NULL) {
      st = api->GetTensorShapeElementCount(info, &n);
    }
    api->ReleaseTensorTypeAndShapeInfo(info);
    if (failed(st, err) || failed(api->GetTensorMutableData(out[i], (void **)&p), err)) {
      goto done;
    }
    if (type != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT) {
      set_err(err, "output isn't float");
      goto done;
    }
    if (n > caps[i]) {
      snprintf(err, HD_ORT_ERRLEN, "output %s has %zu values, room for %zu", names[i], n, caps[i]);
      goto done;
    }
    memcpy(outs[i], p, n * sizeof(float));
    lens[i] = n;
  }
  rc = 0;

done:
  for (int i = 0; i < nout; i++) {
    if (out[i] != NULL) {
      api->ReleaseValue(out[i]);
    }
  }
  api->ReleaseValue(in);
  return rc;
}
