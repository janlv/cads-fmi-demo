package workflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

type s3DownloadRequest struct {
	Bucket         string
	Key            string
	Region         string
	Endpoint       string
	ForcePathStyle bool
}

type s3DownloadFunc func(request s3DownloadRequest, destination string) error

func (e *Executor) buildS3InputSeries(spec s3InputSeriesSpec) (*resolvedInputSeries, error) {
	key := strings.TrimSpace(spec.Key)
	if key == "" {
		return nil, fmt.Errorf("input_series.s3.key is required")
	}

	bucket := pickNonEmpty(spec.Bucket, os.Getenv("S3_BUCKET"))
	if bucket == "" {
		return nil, fmt.Errorf("input_series.s3.bucket is required or S3_BUCKET must be set")
	}

	endpoint := pickNonEmpty(spec.Endpoint, os.Getenv("S3_ENDPOINT"), os.Getenv("AWS_ENDPOINT_URL_S3"), os.Getenv("AWS_ENDPOINT_URL"))
	region := pickNonEmpty(spec.Region, os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"), os.Getenv("S3_REGION"), "us-east-1")
	forcePathStyle := false
	if spec.ForcePathStyle != nil {
		forcePathStyle = *spec.ForcePathStyle
	} else if envValue, ok := parseBoolEnv("S3_FORCE_PATH_STYLE"); ok {
		forcePathStyle = envValue
	} else if endpoint != "" {
		forcePathStyle = true
	}

	file, err := os.CreateTemp("", "cads-s3-input-*.csv")
	if err != nil {
		return nil, fmt.Errorf("create temp input CSV: %w", err)
	}
	tempPath := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(tempPath)
		return nil, fmt.Errorf("close temp input CSV: %w", err)
	}

	request := s3DownloadRequest{
		Bucket:         bucket,
		Key:            key,
		Region:         region,
		Endpoint:       endpoint,
		ForcePathStyle: forcePathStyle,
	}
	if err := e.s3Downloader(request, tempPath); err != nil {
		os.Remove(tempPath)
		return nil, err
	}

	return &resolvedInputSeries{
		Config: &fmi.InputSeriesConfig{CSVPath: tempPath},
		Cleanup: func() {
			_ = os.Remove(tempPath)
		},
	}, nil
}

func defaultS3Downloader(request s3DownloadRequest, destination string) error {
	cfg, err := config.LoadDefaultConfig(
		context.Background(),
		config.WithRegion(request.Region),
	)
	if err != nil {
		return fmt.Errorf("load S3 config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = request.ForcePathStyle
		if request.Endpoint != "" {
			options.BaseEndpoint = aws.String(request.Endpoint)
		}
	})

	output, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(request.Bucket),
		Key:    aws.String(request.Key),
	})
	if err != nil {
		return fmt.Errorf("download s3://%s/%s: %w", request.Bucket, request.Key, err)
	}
	defer output.Body.Close()

	target, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("open destination %s: %w", destination, err)
	}
	defer target.Close()

	if _, err := io.Copy(target, output.Body); err != nil {
		return fmt.Errorf("write destination %s: %w", destination, err)
	}
	return nil
}

func parseBoolEnv(name string) (bool, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false, false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func pickNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
