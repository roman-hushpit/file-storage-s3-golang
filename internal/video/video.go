package video

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func GeneratePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignS3Client := s3.NewPresignClient(s3Client)
	object, err := presignS3Client.PresignGetObject(context.Background(), &s3.GetObjectInput{Bucket: &bucket, Key: &key}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	return object.URL, nil
}
