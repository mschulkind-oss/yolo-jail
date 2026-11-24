package callbroker

import "errors"

const legacyClassifierVersion = "yolo.call-broker-classifier/1"
const maxLookupFacts = 4

// classifierNegotiation belongs to one classification, never a cache or policy.
// A provisional answer has exactly one exit: discard it and send /2 unchanged.
type classifierNegotiation struct {
	legacyLabel string
}

func (n *classifierNegotiation) upgrade(c Classifier, first bool, request, answer string, reads, lookups bool) (bool, error) {
	if !compatibleClassifierVersion(answer, request) {
		return false, errors.New("classifier protocol label mismatches the request; update the pack's classifier")
	}
	if request == legacyClassifierVersion {
		if n.legacyLabel != "" && n.legacyLabel != answer {
			return false, errors.New("classifier changed its advertised protocol label; update the pack's classifier")
		}
		n.legacyLabel = answer
	}
	if !first || request != legacyClassifierVersion || answer != typedClassifierVersion || !reads && !lookups {
		return false, nil
	}
	for _, cell := range c.Cells {
		if cell.IsRead() {
			return true, nil
		}
	}
	return false, nil
}

func validateOperationWireVersion(version string, reads [][]Resource, descriptors []Read, writes []Write) error {
	if version == legacyClassifierVersion {
		if len(descriptors) != 0 {
			return errors.New("answer to /1 must contain legacy read chains; update the pack's classifier")
		}
		for _, write := range writes {
			if write.kindPresent || write.Kind != "" {
				return errors.New("answer to /1 must contain legacy single-cell writes; update the pack's classifier")
			}
		}
		return nil
	}
	if version != typedClassifierVersion {
		return errors.New("unknown classifier request version")
	}
	if len(reads) != len(descriptors) {
		return errors.New("answer to /2 must contain typed read objects; update the pack's classifier")
	}
	for _, descriptor := range descriptors {
		if descriptor.legacy {
			return errors.New("answer to /2 must contain typed read objects; update the pack's classifier")
		}
	}
	return nil
}
