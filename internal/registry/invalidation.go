package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// InvalidationFormat defines the output format for the invalidation file.
type InvalidationFormat string

const (
	InvalidationFormatJSON       InvalidationFormat = "json"
	InvalidationFormatTxt        InvalidationFormat = "txt"
	InvalidationFormatCloudFront InvalidationFormat = "cloudfront"
)

// ParseInvalidationFormat parses a string into an InvalidationFormat.
func ParseInvalidationFormat(s string) (InvalidationFormat, error) {
	switch strings.ToLower(s) {
	case "json":
		return InvalidationFormatJSON, nil
	case "txt":
		return InvalidationFormatTxt, nil
	case "cloudfront":
		return InvalidationFormatCloudFront, nil
	default:
		return "", fmt.Errorf("unsupported invalidation format %q (supported: json, txt, cloudfront)", s)
	}
}

// cloudFrontInvalidationBatch matches the AWS CloudFront create-invalidation --invalidation-batch schema.
type cloudFrontInvalidationBatch struct {
	Paths           cloudFrontPaths `json:"Paths"`
	CallerReference string          `json:"CallerReference"`
}

type cloudFrontPaths struct {
	Quantity int      `json:"Quantity"`
	Items    []string `json:"Items"`
}

// WriteInvalidationFile writes the collected invalidation paths to a file in the given format.
func WriteInvalidationFile(paths []string, filePath string, format InvalidationFormat) error {
	var data []byte
	var err error

	switch format {
	case InvalidationFormatJSON:
		data, err = json.MarshalIndent(paths, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling invalidation paths: %w", err)
		}
		data = append(data, '\n')

	case InvalidationFormatTxt:
		data = []byte(strings.Join(paths, "\n") + "\n")

	case InvalidationFormatCloudFront:
		batch := cloudFrontInvalidationBatch{
			Paths: cloudFrontPaths{
				Quantity: len(paths),
				Items:    paths,
			},
			CallerReference: fmt.Sprintf("tfr-static-%d", time.Now().Unix()),
		}
		data, err = json.MarshalIndent(batch, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling cloudfront invalidation batch: %w", err)
		}
		data = append(data, '\n')

	default:
		return fmt.Errorf("unsupported invalidation format %q", format)
	}

	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("writing invalidation file: %w", err)
	}

	return nil
}
