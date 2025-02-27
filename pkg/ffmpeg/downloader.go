package ffmpeg

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/stashapp/stash/pkg/logger"
	"github.com/stashapp/stash/pkg/utils"
)

func findInPaths(paths []string, baseName string) string {
	for _, p := range paths {
		filePath := filepath.Join(p, baseName)
		if exists, _ := utils.FileExists(filePath); exists {
			return filePath
		}
	}

	return ""
}

func GetPaths(paths []string) (string, string) {
	var ffmpegPath, ffprobePath string

	// Check if ffmpeg exists in the PATH
	if pathBinaryHasCorrectFlags() {
		ffmpegPath, _ = exec.LookPath("ffmpeg")
		ffprobePath, _ = exec.LookPath("ffprobe")
	}

	// Check if ffmpeg exists in the config directory
	if ffmpegPath == "" {
		ffmpegPath = findInPaths(paths, getFFMPEGFilename())
	}
	if ffprobePath == "" {
		ffprobePath = findInPaths(paths, getFFProbeFilename())
	}

	return ffmpegPath, ffprobePath
}

func Download(configDirectory string) error {
	for _, url := range getFFMPEGURL() {
		err := DownloadSingle(configDirectory, url)
		if err != nil {
			return err
		}
	}
	return nil
}

type progressReader struct {
	io.Reader
	lastProgress int64
	bytesRead    int64
	total        int64
}

func (r *progressReader) Read(p []byte) (int, error) {
	read, err := r.Reader.Read(p)
	if err == nil {
		r.bytesRead += int64(read)
		if r.total > 0 {
			progress := int64(float64(r.bytesRead) / float64(r.total) * 100)
			if progress/5 > r.lastProgress {
				logger.Infof("%d%% downloaded...", progress)
				r.lastProgress = progress / 5
			}
		}
	}

	return read, err
}

func DownloadSingle(configDirectory, url string) error {
	if url == "" {
		return fmt.Errorf("no ffmpeg url for this platform")
	}

	// Configure where we want to download the archive
	urlExt := path.Ext(url)
	urlBase := path.Base(url)
	archivePath := filepath.Join(configDirectory, urlBase)
	_ = os.Remove(archivePath) // remove archive if it already exists
	out, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer out.Close()

	logger.Infof("Downloading %s...", url)

	// Make the HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check server response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	reader := &progressReader{
		Reader: resp.Body,
		total:  resp.ContentLength,
	}

	// Write the response to the archive file location
	_, err = io.Copy(out, reader)
	if err != nil {
		return err
	}

	logger.Info("Downloading complete")

	if urlExt == ".zip" {
		logger.Infof("Unzipping %s...", archivePath)
		if err := unzip(archivePath, configDirectory); err != nil {
			return err
		}

		// On OSX or Linux set downloaded files permissions
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			if err := os.Chmod(filepath.Join(configDirectory, "ffmpeg"), 0755); err != nil {
				return err
			}

			if err := os.Chmod(filepath.Join(configDirectory, "ffprobe"), 0755); err != nil {
				return err
			}

			// TODO: In future possible clear xattr to allow running on osx without user intervention
			// TODO: this however may not be required.
			// xattr -c /path/to/binary -- xattr.Remove(path, "com.apple.quarantine")
		}

		logger.Infof("ffmpeg and ffprobe successfully installed in %s", configDirectory)

	} else {
		return fmt.Errorf("ffmpeg was downloaded to %s", archivePath)
	}

	return nil
}

func getFFMPEGURL() []string {
	var urls []string
	switch runtime.GOOS {
	case "darwin":
		urls = []string{"https://evermeet.cx/ffmpeg/ffmpeg-4.3.1.zip", "https://evermeet.cx/ffmpeg/ffprobe-4.3.1.zip"}
	case "linux":
		// TODO: get appropriate arch (arm,arm64,amd64) and xz untar from https://johnvansickle.com/ffmpeg/
		//       or get the ffmpeg,ffprobe zip repackaged ones from  https://ffbinaries.com/downloads
		urls = []string{""}
	case "windows":
		urls = []string{"https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"}
	default:
		urls = []string{""}
	}
	return urls
}

func getFFMPEGFilename() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

func getFFProbeFilename() string {
	if runtime.GOOS == "windows" {
		return "ffprobe.exe"
	}
	return "ffprobe"
}

// Checks if FFMPEG in the path has the correct flags
func pathBinaryHasCorrectFlags() bool {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return false
	}
	bytes, _ := exec.Command(ffmpegPath).CombinedOutput()
	output := string(bytes)
	hasOpus := strings.Contains(output, "--enable-libopus")
	hasVpx := strings.Contains(output, "--enable-libvpx")
	hasX264 := strings.Contains(output, "--enable-libx264")
	hasX265 := strings.Contains(output, "--enable-libx265")
	hasWebp := strings.Contains(output, "--enable-libwebp")
	return hasOpus && hasVpx && hasX264 && hasX265 && hasWebp
}

func unzip(src, configDirectory string) error {
	zipReader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zipReader.Close()

	for _, f := range zipReader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		filename := f.FileInfo().Name()
		if filename != "ffprobe" && filename != "ffmpeg" && filename != "ffprobe.exe" && filename != "ffmpeg.exe" {
			continue
		}

		rc, err := f.Open()

		unzippedPath := filepath.Join(configDirectory, filename)
		unzippedOutput, err := os.Create(unzippedPath)
		if err != nil {
			return err
		}

		_, err = io.Copy(unzippedOutput, rc)
		if err != nil {
			return err
		}

		if err := unzippedOutput.Close(); err != nil {
			return err
		}
	}

	return nil
}
