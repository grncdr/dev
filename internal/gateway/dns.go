package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type DNSProvider interface {
	EnsureLabel(ctx context.Context, label string) error
	RemoveLabel(ctx context.Context, label string) error
}

type Route53Options struct {
	HostedZoneID string
	Domain       string
	Target       string
	TTL          int64
}

type route53Client interface {
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

type Route53Provider struct {
	client route53Client
	opts   Route53Options
}

func NewRoute53Provider(ctx context.Context, opts Route53Options) (*Route53Provider, error) {
	if strings.TrimSpace(opts.HostedZoneID) == "" {
		return nil, errors.New("route53 hosted zone id is required")
	}
	if strings.TrimSpace(opts.Domain) == "" {
		return nil, errors.New("route53 domain is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return nil, errors.New("route53 target is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = 60
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return NewRoute53ProviderWithClient(route53.NewFromConfig(cfg), opts), nil
}

func NewRoute53ProviderWithClient(client route53Client, opts Route53Options) *Route53Provider {
	if opts.TTL <= 0 {
		opts.TTL = 60
	}
	return &Route53Provider{client: client, opts: opts}
}

func (p *Route53Provider) EnsureLabel(ctx context.Context, label string) error {
	return p.change(ctx, label, types.ChangeActionUpsert)
}

func (p *Route53Provider) RemoveLabel(ctx context.Context, label string) error {
	return p.change(ctx, label, types.ChangeActionDelete)
}

func (p *Route53Provider) change(ctx context.Context, label string, action types.ChangeAction) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return errors.New("label is required")
	}
	domain := normalizeDomain(p.opts.Domain)
	target := normalizeDomain(p.opts.Target)
	base := label + "." + domain
	wildcard := "*." + label + "." + domain
	records := []types.Change{
		recordChange(action, base, target, p.opts.TTL),
		recordChange(action, wildcard, target, p.opts.TTL),
	}
	_, err := p.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &p.opts.HostedZoneID,
		ChangeBatch: &types.ChangeBatch{
			Changes: records,
		},
	})
	if err != nil {
		return fmt.Errorf("route53 %s records for label %s: %w", strings.ToLower(string(action)), label, err)
	}
	return nil
}

func recordChange(action types.ChangeAction, name, target string, ttl int64) types.Change {
	return types.Change{
		Action: action,
		ResourceRecordSet: &types.ResourceRecordSet{
			Name: &name,
			Type: types.RRTypeCname,
			TTL:  &ttl,
			ResourceRecords: []types.ResourceRecord{
				{Value: &target},
			},
		},
	}
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimPrefix(domain, ".")
	domain = strings.TrimSuffix(domain, ".")
	return domain + "."
}
