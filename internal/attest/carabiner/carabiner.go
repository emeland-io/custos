// Package carabiner implements attest.Verifier with the carabiner-dev
// libraries: collector parses the envelopes and statements, signer
// verifies DSSE envelopes and Sigstore bundles.
package carabiner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/carabiner-dev/collector/envelope"
	"github.com/carabiner-dev/signer"
	api "github.com/carabiner-dev/signer/api/v1"
	"github.com/carabiner-dev/signer/key"
	"github.com/carabiner-dev/signer/options"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/model"
)

// TrustedKey is a public key loaded from the keys directory.
type TrustedKey struct {
	attest.Key
	provider key.PublicKeyProvider
}

// sharedSigner is created once per process: constructing a signer
// verifier resolves the Sigstore trust roots, which refreshes them through
// TUF when the embedded copy is older than 30 days.
var sharedSigner = sync.OnceValue(func() *signer.Verifier { return signer.NewVerifier() })

// Warmup resolves the Sigstore trust roots, so the first verification
// does not wait for it.
func Warmup() { sharedSigner() }

// Verifier verifies attestations against a set of trusted keys.
type Verifier struct {
	mu     sync.RWMutex
	keys   []TrustedKey
	online bool
}

var _ attest.Verifier = (*Verifier)(nil)

// Option configures a Verifier.
type Option func(*Verifier)

// WithOnline allows Rekor lookups to verify keyless DSSE envelopes.
func WithOnline(online bool) Option {
	return func(v *Verifier) { v.online = online }
}

// WithKeys adds trusted public keys.
func WithKeys(keys ...TrustedKey) Option {
	return func(v *Verifier) { v.keys = append(v.keys, keys...) }
}

// New returns a Verifier.
func New(opts ...Option) *Verifier {
	v := &Verifier{}
	for _, o := range opts {
		o(v)
	}
	return v
}

// Keys returns the trusted keys.
func (v *Verifier) Keys() []TrustedKey {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return slices.Clone(v.keys)
}

// SetKeys replaces the trusted keys.
func (v *Verifier) SetKeys(keys []TrustedKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys = slices.Clone(keys)
}

// NewTrustedKey wraps a public key provider as a TrustedKey.
func NewTrustedKey(file string, p key.PublicKeyProvider) (TrustedKey, error) {
	pub, err := p.PublicKey()
	if err != nil {
		return TrustedKey{}, fmt.Errorf("reading public key: %w", err)
	}
	id := &api.Identity{Key: api.IdentityKeyFromPublic(pub)}
	return TrustedKey{Key: attest.Key{File: file, Identity: id.Spec()}, provider: p}, nil
}

// LoadKeys reads every public key file in dir. A missing dir yields no
// keys.
func LoadKeys(dir string) ([]TrustedKey, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading keys dir: %w", err)
	}
	parser := key.NewParser()
	var keys []TrustedKey
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading key %s: %w", e.Name(), err)
		}
		p, err := parser.ParsePublicKeyProvider(data)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", e.Name(), err)
		}
		tk, err := NewTrustedKey(e.Name(), p)
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", e.Name(), err)
		}
		keys = append(keys, tk)
	}
	return keys, nil
}

// Inspect implements attest.Verifier.
func (v *Verifier) Inspect(raw []byte) (attest.Parsed, error) {
	f := format(raw)
	// The collector wraps any JSON document that is not a statement into
	// a synthetic one, so bare uploads are checked here.
	if f == attest.FormatBare && !isStatement(raw) {
		return attest.Parsed{}, fmt.Errorf("%w: no in-toto statement", attest.ErrNotAttestation)
	}
	envs, err := envelope.Parsers.Parse(bytes.NewReader(raw))
	if err != nil || len(envs) == 0 || envs[0].GetStatement() == nil {
		return attest.Parsed{}, fmt.Errorf("%w: %v", attest.ErrNotAttestation, err)
	}
	st := envs[0].GetStatement()
	p := attest.Parsed{
		Format:        f,
		PredicateType: string(st.GetPredicateType()),
	}
	for _, s := range st.GetSubjects() {
		p.Subjects = append(p.Subjects, model.Subject{
			Name:   s.GetName(),
			URI:    s.GetUri(),
			Digest: s.GetDigest(),
		})
	}
	return p, nil
}

func isStatement(raw []byte) bool {
	var probe struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
	}
	return json.Unmarshal(raw, &probe) == nil &&
		strings.HasPrefix(probe.Type, "https://in-toto.io/Statement/") && probe.PredicateType != ""
}

// format tells DSSE envelopes and Sigstore bundles apart from bare
// statements.
func format(raw []byte) string {
	art, err := signer.ParseArtifact(raw)
	if err != nil {
		return attest.FormatBare
	}
	if art.Kind() == signer.ArtifactKindBundle {
		return attest.FormatBundle
	}
	return attest.FormatDSSE
}

// Verify implements attest.Verifier.
func (v *Verifier) Verify(raw []byte, req model.RequiredAttestation) (attest.Parsed, model.Verification, error) {
	p, err := v.Inspect(raw)
	if err != nil {
		return p, model.Verification{}, err
	}
	res := model.Verification{Date: time.Now().UTC(), Status: model.StatusUnsigned}
	var ver *api.Verification
	if p.Format != attest.FormatBare {
		var providers []key.PublicKeyProvider
		for _, k := range v.Keys() {
			providers = append(providers, k.provider)
		}
		opts := []options.VerificationOptFunc{
			options.WithRekorVerification(v.online),
			// Identities are matched below against the Node's
			// requirement, not by the bundle verifier.
			options.WithSkipIdentityCheck(true),
		}
		if len(providers) > 0 {
			opts = append(opts, options.WithPublicKeys(providers...))
		}
		ver, err = sharedSigner().VerifyStatementBytes(raw, opts...)
		if err != nil {
			return p, model.Verification{}, fmt.Errorf("verifying signatures: %w", err)
		}
		sig := ver.GetSignature()
		res.Status = status(sig.GetStatus())
		res.Error = sig.GetError()
		for _, id := range sig.GetIdentities() {
			res.Identities = append(res.Identities, id.Spec())
		}
	}

	res.RequirementErrors = attest.CheckRequirement(p, req, func(spec string) (bool, error) {
		id, err := api.NewIdentityFromSpec(spec)
		if err != nil {
			return false, err
		}
		return ver.GetVerified() && ver.MatchesIdentity(id), nil
	})
	res.RequirementMet = res.Status == model.StatusVerified && len(res.RequirementErrors) == 0
	return p, res, nil
}

func status(s api.VerificationStatus) model.VerificationStatus {
	switch s {
	case api.VerificationStatus_VERIFIED:
		return model.StatusVerified
	case api.VerificationStatus_FAILED:
		return model.StatusFailed
	case api.VerificationStatus_UNSIGNED:
		return model.StatusUnsigned
	default:
		return model.StatusUnverifiable
	}
}
