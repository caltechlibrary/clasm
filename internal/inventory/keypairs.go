package inventory

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// KeyPair is an EC2 key pair as displayed/managed by awsops, aggregated
// across regions (DESIGN.md, Feature 13: "List Key Pairs").
type KeyPair struct {
	KeyName        string
	KeyPairID      string
	KeyFingerprint string
	KeyType        string
	Region         string
}

// ListKeyPairs queries ec2:DescribeKeyPairs in each region concurrently
// and aggregates the results. DescribeKeyPairs is not paginated, unlike
// DescribeInstances/DescribeImages, so listKeyPairsInRegion makes a
// single call per region.
func ListKeyPairs(ctx context.Context, clients map[string]awsclient.EC2API) ([]KeyPair, error) {
	type result struct {
		region   string
		keyPairs []KeyPair
		err      error
	}

	results := make(chan result, len(clients))
	var wg sync.WaitGroup
	for region, client := range clients {
		wg.Add(1)
		go func(region string, client awsclient.EC2API) {
			defer wg.Done()
			keyPairs, err := listKeyPairsInRegion(ctx, client, region)
			results <- result{region: region, keyPairs: keyPairs, err: err}
		}(region, client)
	}
	wg.Wait()
	close(results)

	var all []KeyPair
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", r.region, r.err)
		}
		all = append(all, r.keyPairs...)
	}
	return all, nil
}

func listKeyPairsInRegion(ctx context.Context, client awsclient.EC2API, region string) ([]KeyPair, error) {
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return nil, err
	}
	keyPairs := make([]KeyPair, 0, len(out.KeyPairs))
	for _, kp := range out.KeyPairs {
		keyPairs = append(keyPairs, keyPairFromSDK(kp, region))
	}
	return keyPairs, nil
}

func keyPairFromSDK(kp types.KeyPairInfo, region string) KeyPair {
	return KeyPair{
		KeyName:        aws.ToString(kp.KeyName),
		KeyPairID:      aws.ToString(kp.KeyPairId),
		KeyFingerprint: aws.ToString(kp.KeyFingerprint),
		KeyType:        string(kp.KeyType),
		Region:         region,
	}
}
