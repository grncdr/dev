package gateway

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type fakeRoute53Client struct {
	last *route53.ChangeResourceRecordSetsInput
}

func (f *fakeRoute53Client) ChangeResourceRecordSets(_ context.Context, in *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	f.last = in
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}

func TestRoute53ProviderEnsureLabel(t *testing.T) {
	t.Parallel()

	client := &fakeRoute53Client{}
	provider := NewRoute53ProviderWithClient(client, Route53Options{
		HostedZoneID: "Z123",
		Domain:       "tunnels.example.test",
		Target:       "tunnels.example.test",
		TTL:          60,
	})
	if err := provider.EnsureLabel(context.Background(), "alpha"); err != nil {
		t.Fatalf("ensure label: %v", err)
	}
	if client.last == nil {
		t.Fatalf("expected route53 change call")
	}
	if *client.last.HostedZoneId != "Z123" {
		t.Fatalf("unexpected hosted zone id: %s", *client.last.HostedZoneId)
	}
	if len(client.last.ChangeBatch.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(client.last.ChangeBatch.Changes))
	}
	if client.last.ChangeBatch.Changes[0].Action != types.ChangeActionUpsert {
		t.Fatalf("expected UPSERT")
	}
}
