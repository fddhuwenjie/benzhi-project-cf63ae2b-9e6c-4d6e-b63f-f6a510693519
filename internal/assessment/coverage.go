package assessment

import (
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type CoverageCell struct {
	EvidenceID         string   `json:"evidence_id"`
	InstrumentKind     string   `json:"instrument_kind"`
	Covered            bool     `json:"covered"`
	IssueCode          string   `json:"issue_code,omitempty"`
	QualificationIDs   []string `json:"qualification_ids"`
	QualificationID    string   `json:"qualification_id,omitempty"`
	InstrumentID       string   `json:"instrument_id,omitempty"`
	CertificateVersion int      `json:"certificate_version,omitempty"`
	CertificateDigest  string   `json:"certificate_digest,omitempty"`
	CalibrationValid   bool     `json:"calibration_valid"`
	UsageIntervalValid bool     `json:"usage_interval_valid"`
}
type CoverageKindSummary struct {
	InstrumentKind       string   `json:"instrument_kind"`
	QualifiedCount       int      `json:"qualified_count"`
	UncoveredEvidenceIDs []string `json:"uncovered_evidence_ids"`
}
type CoverageMatrix struct {
	Cells         []CoverageCell        `json:"cells"`
	Kinds         []CoverageKindSummary `json:"kinds"`
	BlockingCodes []string              `json:"blocking_codes"`
	Verdict       string                `json:"verdict"`
}

func requiredKinds(t string) []string {
	return domain.RequiredInstrumentKinds(t)
}

func (e *Engine) CoverageMatrix(evidence []domain.Evidence, qualifications []domain.Qualification) CoverageMatrix {
	m := CoverageMatrix{Cells: []CoverageCell{}, Kinds: []CoverageKindSummary{}, BlockingCodes: []string{}, Verdict: "pass"}
	uncovered := map[string][]string{}
	counts := map[string]int{}
	codes := map[string]bool{}
	for i := 0; i < len(qualifications); i++ {
		for j := i + 1; j < len(qualifications); j++ {
			a, b := qualifications[i], qualifications[j]
			if a.InstrumentID == b.InstrumentID && a.CertificateRef != b.CertificateRef && intervalsOverlap(a.CalibratedAt, a.ValidUntil, b.CalibratedAt, b.ValidUntil) {
				codes["certificate_interval_conflict"] = true
				m.Verdict = "fail"
				ids := []string{a.QualificationID, b.QualificationID}
				sort.Strings(ids)
				m.Cells = append(m.Cells, CoverageCell{InstrumentKind: a.InstrumentKind, IssueCode: "certificate_interval_conflict", QualificationIDs: ids})
			}
		}
	}
	for _, q := range qualifications {
		if q.Verdict == "qualified" {
			counts[q.InstrumentKind]++
		}
	}
	for _, item := range evidence {
		for _, kind := range requiredKinds(item.EvidenceType) {
			cell := CoverageCell{EvidenceID: item.EvidenceID, InstrumentKind: kind, QualificationIDs: []string{}}
			bestCode := "instrument_unbound"
			var binding *domain.InstrumentBinding
			for i := range item.InstrumentBindings {
				if item.InstrumentBindings[i].InstrumentKind == kind {
					binding = &item.InstrumentBindings[i]
					break
				}
			}
			if binding != nil {
				cell.QualificationID, cell.InstrumentID, cell.CertificateDigest = binding.QualificationID, binding.InstrumentID, binding.CertificateDigest
				cell.QualificationIDs = append(cell.QualificationIDs, binding.QualificationID)
				bestCode = "qualification_not_found"
				for _, q := range qualifications {
					if q.QualificationID != binding.QualificationID {
						continue
					}
					cell.CertificateVersion = q.Version
					if q.InstrumentID != binding.InstrumentID || q.InstrumentKind != kind {
						bestCode = "instrument_mismatch"
						break
					}
					if binding.CertificateDigest != q.Digest {
						bestCode = "stale_certificate_digest"
						break
					}
					if item.ObservedAt.Before(q.CalibratedAt) || item.ObservedAt.After(q.ValidUntil) {
						bestCode = "coverage_expired"
						break
					}
					cell.CalibrationValid = true
					if item.ObservedAt.Before(q.UsageStartedAt) || item.ObservedAt.After(q.UsageEndedAt) {
						bestCode = "usage_interval_out_of_bounds"
						break
					}
					cell.UsageIntervalValid = true
					if q.Verdict != "qualified" {
						bestCode = "qualification_unqualified"
						break
					}
					cell.Covered, bestCode = true, ""
					break
				}
			}
			if !cell.Covered {
				cell.IssueCode = bestCode
				uncovered[kind] = append(uncovered[kind], item.EvidenceID)
				codes[bestCode] = true
				m.Verdict = "fail"
			}
			sort.Strings(cell.QualificationIDs)
			m.Cells = append(m.Cells, cell)
		}
	}
	for _, kind := range []string{"current_meter", "water_level_gauge", "survey_equipment"} {
		ids := uncovered[kind]
		sort.Strings(ids)
		m.Kinds = append(m.Kinds, CoverageKindSummary{kind, counts[kind], ids})
	}
	for code := range codes {
		m.BlockingCodes = append(m.BlockingCodes, code)
	}
	sort.Strings(m.BlockingCodes)
	sort.Slice(m.Cells, func(i, j int) bool {
		if m.Cells[i].EvidenceID == m.Cells[j].EvidenceID {
			return m.Cells[i].InstrumentKind < m.Cells[j].InstrumentKind
		}
		return m.Cells[i].EvidenceID < m.Cells[j].EvidenceID
	})
	return m
}

func intervalsOverlap(a1, a2, b1, b2 time.Time) bool { return !a1.After(b2) && !b1.After(a2) }
