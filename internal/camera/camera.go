// Package camera opens the configured V4L2 camera for the frame loops
package camera

import (
	"sync"
	"time"

	"howdy-go/internal/config"
	"howdy-go/internal/imgproc"
	"howdy-go/internal/v4l2"
)

// Camera reads grey frames from a V4L2 device
type Camera struct {
	cam  *v4l2.Camera
	once sync.Once
}

// Open opens and starts the camera from the [video] settings
func Open(s *config.Settings) (*Camera, error) {
	dev, err := v4l2.Open(s.DevicePath)
	if err != nil {
		return nil, err
	}
	cam, err := v4l2.Start(dev, s.FrameWidth, s.FrameHeight, s.DeviceFPS)
	if err != nil {
		return nil, err
	}
	return &Camera{cam: cam}, nil
}

// Read returns the newest frame, a fresh image every time
func (c *Camera) Read(timeout time.Duration) (*imgproc.Gray, error) {
	pix, err := c.cam.Read(timeout, nil)
	if err != nil {
		return nil, err
	}
	return &imgproc.Gray{W: c.cam.Width, H: c.cam.Height, Pix: pix}, nil
}

// SetExposure switches to manual exposure at value
func (c *Camera) SetExposure(value int) error {
	if err := c.cam.SetControl(v4l2.CIDExposureAuto, v4l2.ExposureManual); err != nil {
		return err
	}
	return c.cam.SetControl(v4l2.CIDExposureAbsolute, int32(value))
}

// Close releases the camera, more than once is fine
func (c *Camera) Close() {
	c.once.Do(c.cam.Close)
}
