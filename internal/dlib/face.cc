//go:build !nodlib

// The dlib calls Howdy's Python code makes, matched to dlib's own Python
// bindings (tools/python/src in the dlib source): face_recognition.cpp,
// simple_object_detector_py.h, shape_predictor.h and cnn_face_detector.cpp.

#include "face.h"

#include <dlfcn.h>

#include <cstring>
#include <exception>
#include <memory>
#include <string>

#include <dlib/dnn.h>
#include <dlib/image_processing.h>
#include <dlib/image_processing/frontal_face_detector.h>
#include <dlib/serialize.h>

using namespace dlib;

namespace {

// The ResNet from dlib's dnn_face_recognition_ex.cpp, as in face_recognition.cpp
template <template <int, template <typename> class, int, typename> class block,
          int N, template <typename> class BN, typename SUBNET>
using residual = add_prev1<block<N, BN, 1, tag1<SUBNET>>>;

template <template <int, template <typename> class, int, typename> class block,
          int N, template <typename> class BN, typename SUBNET>
using residual_down =
    add_prev2<avg_pool<2, 2, 2, 2, skip1<tag2<block<N, BN, 2, tag1<SUBNET>>>>>>;

template <int N, template <typename> class BN, int stride, typename SUBNET>
using block = BN<con<N, 3, 3, 1, 1, relu<BN<con<N, 3, 3, stride, stride, SUBNET>>>>>;

template <int N, typename SUBNET> using ares = relu<residual<block, N, affine, SUBNET>>;
template <int N, typename SUBNET> using ares_down = relu<residual_down<block, N, affine, SUBNET>>;

template <typename SUBNET> using alevel0 = ares_down<256, SUBNET>;
template <typename SUBNET> using alevel1 = ares<256, ares<256, ares_down<256, SUBNET>>>;
template <typename SUBNET> using alevel2 = ares<128, ares<128, ares_down<128, SUBNET>>>;
template <typename SUBNET> using alevel3 = ares<64, ares<64, ares<64, ares_down<64, SUBNET>>>>;
template <typename SUBNET> using alevel4 = ares<32, ares<32, ares<32, SUBNET>>>;

using anet_type = loss_metric<fc_no_bias<
    128, avg_pool_everything<alevel0<alevel1<alevel2<alevel3<alevel4<max_pool<
             3, 3, 2, 2, relu<affine<con<32, 7, 7, 2, 2, input_rgb_image_sized<150>>>>>>>>>>>>>;

// The MMOD detector from cnn_face_detector.cpp
template <long num_filters, typename SUBNET> using con5d = con<num_filters, 5, 5, 2, 2, SUBNET>;
template <long num_filters, typename SUBNET> using con5 = con<num_filters, 5, 5, 1, 1, SUBNET>;
template <typename SUBNET>
using downsampler =
    relu<affine<con5d<32, relu<affine<con5d<32, relu<affine<con5d<16, SUBNET>>>>>>>>>;
template <typename SUBNET> using rcon5 = relu<affine<con5<45, SUBNET>>>;
using mmod_type = loss_mmod<
    con<1, 9, 9, 1, 1, rcon5<rcon5<rcon5<downsampler<input_rgb_image_pyramid<pyramid_down<6>>>>>>>>;

// What dlib.fhog_object_detector and get_frontal_face_detector() hold
using hog_type = object_detector<scan_fhog_pyramid<pyramid_down<6>>>;

void set_err(char *err, const char *msg) {
  if (err == nullptr) {
    return;
  }
  std::strncpy(err, msg, HD_ERRLEN - 1);
  err[HD_ERRLEN - 1] = '\0';
}

// Wraps a call so no C++ exception crosses into Go
template <typename F> int guarded(char *err, F &&fn) {
  try {
    return fn();
  } catch (const std::exception &e) {
    set_err(err, e.what());
  } catch (...) {
    set_err(err, "unknown dlib error");
  }
  return -1;
}

void to_rgb(const uint8_t *grey, int w, int h, matrix<rgb_pixel> &out) {
  out.set_size(h, w);
  for (long y = 0; y < h; y++) {
    for (long x = 0; x < w; x++) {
      uint8_t v = grey[y * w + x];
      out(y, x) = rgb_pixel(v, v, v);
    }
  }
}

void to_grey(const uint8_t *grey, int w, int h, array2d<unsigned char> &out) {
  out.set_size(h, w);
  for (long y = 0; y < h; y++) {
    std::memcpy(&out[y][0], grey + y * w, w);
  }
}

} // namespace

struct hd_face {
  std::unique_ptr<hog_type> hog;
  std::unique_ptr<mmod_type> cnn;
  std::unique_ptr<shape_predictor> shape;
  std::unique_ptr<anet_type> encoder;
};

extern "C" {

hd_face *hd_new(void) {
  try {
    return new hd_face();
  } catch (...) {
    return nullptr;
  }
}

void hd_free(hd_face *f) { delete f; }

int hd_load_hog(hd_face *f, const char *path, char *err) {
  return guarded(err, [&] {
    auto d = std::make_unique<hog_type>();
    deserialize(path) >> *d;
    f->hog = std::move(d);
    return 0;
  });
}

int hd_use_builtin_hog(hd_face *f, char *err) {
  return guarded(err, [&] {
    f->hog = std::make_unique<hog_type>(get_frontal_face_detector());
    return 0;
  });
}

int hd_load_cnn(hd_face *f, const char *path, char *err) {
  return guarded(err, [&] {
    auto d = std::make_unique<mmod_type>();
    deserialize(path) >> *d;
    f->cnn = std::move(d);
    return 0;
  });
}

int hd_load_shape(hd_face *f, const char *path, char *err) {
  return guarded(err, [&] {
    auto d = std::make_unique<shape_predictor>();
    deserialize(path) >> *d;
    f->shape = std::move(d);
    return 0;
  });
}

int hd_load_encoder(hd_face *f, const char *path, char *err) {
  return guarded(err, [&] {
    auto d = std::make_unique<anet_type>();
    deserialize(path) >> *d;
    f->encoder = std::move(d);
    return 0;
  });
}

int hd_detect(hd_face *f, const uint8_t *grey, int w, int h, int upsample,
              int use_cnn, long *rects, int max, char *err) {
  return guarded(err, [&] {
    if (w <= 0 || h <= 0 || upsample < 0) {
      throw std::runtime_error("bad image size or upsample");
    }
    pyramid_down<2> pyr;
    std::vector<rectangle> found;

    if (use_cnn) {
      if (!f->cnn) {
        throw std::runtime_error("CNN detector not loaded");
      }
      matrix<rgb_pixel> image;
      to_rgb(grey, w, h, image);
      for (int i = 0; i < upsample; i++) {
        pyramid_up(image, pyr);
      }
      for (auto &d : (*f->cnn)(image)) {
        found.push_back(pyr.rect_down(d.rect, upsample));
      }
    } else {
      if (!f->hog) {
        throw std::runtime_error("HOG detector not loaded");
      }
      array2d<unsigned char> image;
      to_grey(grey, w, h, image);
      std::vector<rect_detection> dets;
      if (upsample == 0) {
        (*f->hog)(image, dets, 0.0);
      } else {
        array2d<unsigned char> temp;
        pyramid_up(image, temp, pyr);
        for (int i = 1; i < upsample; i++) {
          pyramid_up(temp);
        }
        (*f->hog)(temp, dets, 0.0);
        for (auto &d : dets) {
          d.rect = pyr.rect_down(d.rect, upsample);
        }
      }
      for (auto &d : dets) {
        found.push_back(d.rect);
      }
    }

    for (int i = 0; i < (int)found.size() && i < max; i++) {
      rects[i * 4 + 0] = found[i].left();
      rects[i * 4 + 1] = found[i].top();
      rects[i * 4 + 2] = found[i].right();
      rects[i * 4 + 3] = found[i].bottom();
    }
    return (int)found.size();
  });
}

int hd_encode(hd_face *f, const uint8_t *grey, int w, int h, const long *rect,
              double *descriptor, long *parts, char *err) {
  return guarded(err, [&] {
    if (!f->shape || !f->encoder) {
      throw std::runtime_error("shape predictor or encoder not loaded");
    }
    matrix<rgb_pixel> image;
    to_rgb(grey, w, h, image);
    rectangle box(rect[0], rect[1], rect[2], rect[3]);

    full_object_detection shape = (*f->shape)(image, box);
    if (shape.num_parts() != 5) {
      throw std::runtime_error("expected the 5 point shape predictor");
    }
    for (unsigned long i = 0; i < 5; i++) {
      parts[i * 2] = shape.part(i).x();
      parts[i * 2 + 1] = shape.part(i).y();
    }

    // num_jitters 1 in compare.py, which the binding treats as no jitter
    matrix<rgb_pixel> chip;
    extract_image_chip(image, get_face_chip_details(shape, 150, 0.25), chip);
    std::vector<matrix<rgb_pixel>> chips{chip};
    auto out = (*f->encoder)(chips, 16);
    matrix<double, 0, 1> d = matrix_cast<double>(out[0]);
    if (d.size() != 128) {
      throw std::runtime_error("encoder gave the wrong size");
    }
    for (long i = 0; i < 128; i++) {
      descriptor[i] = d(i);
    }
    return 0;
  });
}

int hd_save_frontal(const char *path, char *err) {
  return guarded(err, [&] {
    hog_type d = get_frontal_face_detector();
    serialize(path) << d;
    return 0;
  });
}

int hd_blas_single_thread(void) {
  using setter = void (*)(int);
  auto set = reinterpret_cast<setter>(dlsym(RTLD_DEFAULT, "openblas_set_num_threads"));
  if (set == nullptr) {
    return 0;
  }
  set(1);
  return 1;
}

} // extern "C"
