package storage

import (
	"encoding/json"
	"fmt"

	"github.com/docker/docker-registry/storagedriver"
	"github.com/docker/libtrust"
)

type manifestStore struct {
	driver       storagedriver.StorageDriver
	pathMapper   *pathMapper
	layerService LayerService
}

var _ ManifestService = &manifestStore{}

func (ms *manifestStore) Exists(name, tag string) (bool, error) {
	p, err := ms.path(name, tag)
	if err != nil {
		return false, err
	}

	size, err := ms.driver.CurrentSize(p)
	if err != nil {
		return false, err
	}

	if size == 0 {
		return false, nil
	}

	return true, nil
}

func (ms *manifestStore) Get(name, tag string) (*SignedManifest, error) {
	p, err := ms.path(name, tag)
	if err != nil {
		return nil, err
	}

	content, err := ms.driver.GetContent(p)
	if err != nil {
		switch err := err.(type) {
		case storagedriver.PathNotFoundError, *storagedriver.PathNotFoundError:
			return nil, ErrUnknownManifest{Name: name, Tag: tag}
		default:
			return nil, err
		}
	}

	var manifest SignedManifest

	if err := json.Unmarshal(content, &manifest); err != nil {
		// TODO(stevvooe): Corrupted manifest error?
		return nil, err
	}

	// TODO(stevvooe): Verify the manifest here?

	return &manifest, nil
}

func (ms *manifestStore) Put(name, tag string, manifest *SignedManifest) error {
	p, err := ms.path(name, tag)
	if err != nil {
		return err
	}

	if err := ms.verifyManifest(name, tag, manifest); err != nil {
		return err
	}

	// TODO(stevvooe): Should we get old manifest first? Perhaps, write, then
	// move to ensure a valid manifest?

	return ms.driver.PutContent(p, manifest.Raw)
}

func (ms *manifestStore) Delete(name, tag string) error {
	p, err := ms.path(name, tag)
	if err != nil {
		return err
	}

	if err := ms.driver.Delete(p); err != nil {
		switch err := err.(type) {
		case storagedriver.PathNotFoundError, *storagedriver.PathNotFoundError:
			return ErrUnknownManifest{Name: name, Tag: tag}
		default:
			return err
		}
	}

	return nil
}

func (ms *manifestStore) path(name, tag string) (string, error) {
	return ms.pathMapper.path(manifestPathSpec{
		name: name,
		tag:  tag,
	})
}

func (ms *manifestStore) verifyManifest(name, tag string, manifest *SignedManifest) error {
	// TODO(stevvooe): This verification is present here, but this needs to be
	// lifted out of the storage infrastructure and moved into a package
	// oriented towards defining verifiers and reporting them with
	// granularity.

	var errs ErrManifestVerification
	if manifest.Name != name {
		// TODO(stevvooe): This needs to be an exported error
		errs = append(errs, fmt.Errorf("name does not match manifest name"))
	}

	if manifest.Tag != tag {
		// TODO(stevvooe): This needs to be an exported error.
		errs = append(errs, fmt.Errorf("tag does not match manifest tag"))
	}

	// TODO(stevvooe): These pubkeys need to be checked with either Verify or
	// VerifyWithChains. We need to define the exact source of the CA.
	// Perhaps, its a configuration value injected into manifest store.

	if _, err := manifest.Verify(); err != nil {
		switch err {
		case libtrust.ErrMissingSignatureKey, libtrust.ErrInvalidJSONContent, libtrust.ErrMissingSignatureKey:
			errs = append(errs, ErrManifestUnverified{})
		default:
			if err.Error() == "invalid signature" { // TODO(stevvooe): This should be exported by libtrust
				errs = append(errs, ErrManifestUnverified{})
			} else {
				errs = append(errs, err)
			}
		}
	}

	for _, fsLayer := range manifest.FSLayers {
		exists, err := ms.layerService.Exists(name, fsLayer.BlobSum)
		if err != nil {
			errs = append(errs, err)
		}

		if !exists {
			errs = append(errs, ErrUnknownLayer{FSLayer: fsLayer})
		}
	}

	if len(errs) != 0 {
		// TODO(stevvooe): These need to be recoverable by a caller.
		return errs
	}

	return nil
}
