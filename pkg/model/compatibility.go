package model

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/replicate/modelserver/pkg/version"
)

type TFCompatibility struct {
	TF           string
	TFCPUPackage string
	TFGPUPackage string
	CUDA         string
	CuDNN        string
	Pythons      []string
}

func (compat *TFCompatibility) UnmarshalJSON(data []byte) error {
	// to avoid unmarshalling stack overflow https://stackoverflow.com/questions/34859449/unmarshaljson-results-in-stack-overflow
	type tempType TFCompatibility
	c := new(tempType)
	if err := json.Unmarshal(data, c); err != nil {
		return err
	}
	cuda := version.MustVersion(c.CUDA)
	cuDNN := version.MustVersion(c.CuDNN)
	compat.TF = c.TF
	compat.TFCPUPackage = c.TFCPUPackage
	compat.TFGPUPackage = c.TFGPUPackage
	// include minor version
	compat.CUDA = fmt.Sprintf("%d.%d", cuda.Major, cuda.Minor)
	// strip cuDNN minor version to match nvidia images
	compat.CuDNN = fmt.Sprintf("%d", cuDNN.Major)
	compat.Pythons = c.Pythons
	return nil
}

type TorchCompatibility struct {
	Torch       string
	Torchvision string
	Torchaudio  string
	IndexURL    string
	CUDA        *string
	Pythons     []string
}

func (c *TorchCompatibility) TorchVersion() string {
	parts := strings.Split(c.Torch, "+")
	return parts[0]
}

func (c *TorchCompatibility) TorchvisionVersion() string {
	parts := strings.Split(c.Torchvision, "+")
	return parts[0]
}

type CUDABaseImage struct {
	Tag     string
	CUDA    string
	CuDNN   string
	IsDevel bool
	Ubuntu  string
}

func (i *CUDABaseImage) UnmarshalJSON(data []byte) error {
	var tag string
	if err := json.Unmarshal(data, &tag); err != nil {
		return err
	}
	parts := strings.Split(tag, "-")
	if len(parts) != 4 {
		return fmt.Errorf("Tag must be in the format <cudaVersion>-cudnn<cudnnVersion>-{devel,runtime}-ubuntu<ubuntuVersion>. Invalid tag: %s", tag)
	}
	i.Tag = tag
	i.CUDA = parts[0]
	i.CuDNN = strings.Split(parts[1], "cudnn")[1]
	i.IsDevel = parts[2] == "devel"
	i.Ubuntu = strings.Split(parts[3], "ubuntu")[1]
	return nil
}

func (i *CUDABaseImage) ImageTag() string {
	return "nvidia/cuda:" + i.Tag
}

//go:generate go run ../../cmd/generate_compatibility_matrices/main.go -tf-output tf_compatability_matrix.json -torch-output torch_compatability_matrix.json -cuda-images-output cuda_base_image_tags.json

//go:embed tf_compatability_matrix.json
var tfCompatibilityMatrixData []byte
var TFCompatibilityMatrix []TFCompatibility

//go:embed torch_compatability_matrix.json
var torchCompatibilityMatrixData []byte
var TorchCompatibilityMatrix []TorchCompatibility

//go:embed cuda_base_image_tags.json
var cudaBaseImageTagsData []byte
var CUDABaseImages []CUDABaseImage

func init() {
	if err := json.Unmarshal(tfCompatibilityMatrixData, &TFCompatibilityMatrix); err != nil {
		log.Fatalf("Failed to load embedded Tensorflow compatibility matrix: %s", err)
	}
	if err := json.Unmarshal(torchCompatibilityMatrixData, &TorchCompatibilityMatrix); err != nil {
		log.Fatalf("Failed to load embedded PyTorch compatibility matrix: %s", err)
	}
	if err := json.Unmarshal(cudaBaseImageTagsData, &CUDABaseImages); err != nil {
		log.Fatalf("Failed to load embedded CUDA base images: %s", err)
	}
}

func cudasFromTorch(ver string) ([]string, error) {
	cudas := []string{}
	for _, compat := range TorchCompatibilityMatrix {
		if ver == compat.TorchVersion() && compat.CUDA != nil {
			cudas = append(cudas, *compat.CUDA)
		}
	}
	if len(cudas) == 0 {
		return nil, fmt.Errorf("torch==%s doesn't have any compatible CUDA versions", ver)
	}
	return cudas, nil
}

func cudaFromTF(ver string) (cuda string, cuDNN string, err error) {
	for _, compat := range TFCompatibilityMatrix {
		if ver == compat.TF {
			return compat.CUDA, compat.CuDNN, nil
		}
	}
	return "", "", fmt.Errorf("tensorflow==%s doesn't have any compatible CUDA versions", ver)
}

func compatibleCuDNNsForCUDA(cuda string) []string {
	cuDNNs := []string{}
	for _, image := range CUDABaseImages {
		if image.CUDA == cuda {
			cuDNNs = append(cuDNNs, image.CuDNN)
		}
	}
	return cuDNNs
}

func defaultCUDA() string {
	return latestTF().CUDA
}

func latestCUDAFrom(cudas []string) string {
	latest := ""
	for _, cuda := range cudas {
		if latest == "" {
			latest = cuda
		} else {
			greater, err := versionGreater(cuda, latest)
			if err != nil {
				// should never happen
				panic(fmt.Sprintf("Invalid CUDA version: %s", err))
			}
			if greater {
				latest = cuda
			}
		}
	}
	return latest
}

func latestCuDNNForCUDA(cuda string) string {
	cuDNNs := []string{}
	for _, image := range CUDABaseImages {
		if image.CUDA == cuda {
			cuDNNs = append(cuDNNs, image.CuDNN)
		}
	}
	sort.Slice(cuDNNs, func(i, j int) bool {
		return version.MustVersion(cuDNNs[i]).Greater(version.MustVersion(cuDNNs[j]))
	})
	return cuDNNs[0]
}

func latestTF() TFCompatibility {
	var latest *TFCompatibility
	for _, compat := range TFCompatibilityMatrix {
		if latest == nil {
			latest = &compat
		} else {
			greater, err := versionGreater(latest.TF, compat.TF)
			if err != nil {
				// should never happen
				panic(fmt.Sprintf("Invalid tensorflow version: %s", err))
			}
			if greater {
				latest = &compat
			}
		}
	}
	return *latest
}

func versionGreater(a string, b string) (bool, error) {
	// TODO(andreas): use library
	aVer, err := version.NewVersion(a)
	if err != nil {
		return false, err
	}
	bVer, err := version.NewVersion(b)
	if err != nil {
		return false, err
	}
	return aVer.Greater(bVer), nil
}

func CUDABaseImageFor(cuda string, cuDNN string) (string, error) {
	for _, image := range CUDABaseImages {
		if image.CUDA == cuda && image.CuDNN == cuDNN {
			return image.ImageTag(), nil
		}
	}
	return "", fmt.Errorf("No matching base image for CUDA %s and CuDNN %s", cuda, cuDNN)
}

func tfCPUPackage(ver string) (name string, cpuVersion string, err error) {
	for _, compat := range TFCompatibilityMatrix {
		if compat.TF == ver {
			return splitPythonPackage(compat.TFCPUPackage)
		}
	}
	return "", "", fmt.Errorf("No matching tensorflow CPU package for version %s", ver)
}

func tfGPUPackage(ver string, cuda string) (name string, cpuVersion string, err error) {
	for _, compat := range TFCompatibilityMatrix {
		if compat.TF == ver && compat.CUDA == cuda {
			return splitPythonPackage(compat.TFGPUPackage)
		}
	}
	return "", "", fmt.Errorf("No matching tensorflow GPU package for version %s and CUDA %s", ver, cuda)
}

func torchCPUPackage(ver string) (name string, cpuVersion string, indexURL string, err error) {
	for _, compat := range TorchCompatibilityMatrix {
		if compat.TorchVersion() == ver && compat.CUDA == nil {
			return "torch", compat.Torch, compat.IndexURL, nil
		}
	}
	return "", "", "", fmt.Errorf("No matching Torch CPU package for version %s", ver)
}

func torchGPUPackage(ver string, cuda string) (name string, cpuVersion string, indexURL string, err error) {
	for _, compat := range TorchCompatibilityMatrix {
		if compat.TorchVersion() == ver && compat.CUDA != nil && *compat.CUDA == cuda {
			return "torch", compat.Torch, compat.IndexURL, nil
		}
	}
	return "", "", "", fmt.Errorf("No matching torch GPU package for version %s and CUDA %s", ver, cuda)
}

func torchvisionCPUPackage(ver string) (name string, cpuVersion string, indexURL string, err error) {
	for _, compat := range TorchCompatibilityMatrix {
		if compat.TorchvisionVersion() == ver && compat.CUDA == nil {
			return "torchvision", compat.Torchvision, compat.IndexURL, nil
		}
	}
	return "", "", "", fmt.Errorf("No matching torchvision CPU package for version %s", ver)
}

func torchvisionGPUPackage(ver string, cuda string) (name string, cpuVersion string, indexURL string, err error) {
	for _, compat := range TorchCompatibilityMatrix {
		if compat.TorchvisionVersion() == ver && *compat.CUDA == cuda {
			return "torchvision", compat.Torchvision, compat.IndexURL, nil
		}
	}
	return "", "", "", fmt.Errorf("No matching torchvision GPU package for version %s and CUDA %s", ver, cuda)
}