package incremental

import "fmt"

// LookupDelta prepares a bounded fresh/refresh batch of lookup queries and their
// uniquely owned cells. It does not discover changed/retired declarations or
// refresh previously admitted cells omitted from lookups. The compiler collector
// must supply that manifest separately before serving retained queries. Limits
// include cell-owner query slots as well as actual lookup queries. Admission in
// a nonempty Session can still reject this batch against the projected world.
func (c *FrameCatalog) LookupDelta(lookups []FrameLookup, limits Limits) (Delta, error) {
	if c == nil || c.frames == nil || limits.Queries < 0 || limits.Inputs < 0 || limits.InputEdges < 0 || limits.Dependencies < 0 || limits.Bytes <= 0 || len(lookups) > limits.Queries {
		return Delta{}, fmt.Errorf("catalog batch work budget exceeded")
	}
	d := Delta{}
	seen := map[Key]bool{}
	preparation, bytes, inputs, edges, dependencies := 0, 0, 0, 0, 0
	for _, lookup := range lookups {
		// Preflight aggregate allocation work BEFORE materializing another plan.
		// Duplicate lookup requests still consume preparation work, bounding callers
		// that repeat a small admitted set many times. No integer multiplication here.
		estimate, err := c.lookupPublicationBudget(lookup)
		if err != nil {
			return Delta{}, err
		}
		if estimate > limits.Bytes-preparation {
			return Delta{}, fmt.Errorf("catalog batch preparation budget exceeded")
		}
		preparation += estimate
		plan, err := c.PrepareLookup(lookup)
		if err != nil {
			return Delta{}, err
		}
		publications := append(plan.Cells, plan.Query)
		for _, p := range publications {
			if seen[p.Key] {
				continue
			}
			cost, err := deltaQueryCost(p, len(p.Inputs), limits.Bytes)
			if err != nil {
				return Delta{}, err
			}
			if len(d.Upsert) >= limits.Queries || len(p.Inputs) > limits.Inputs-inputs || len(p.Inputs) > limits.InputEdges-edges || p.MaxDependencies > limits.Dependencies-dependencies || cost > limits.Bytes-bytes {
				return Delta{}, fmt.Errorf("catalog owner batch budget exceeded")
			}
			bytes += cost
			for _, change := range p.Inputs {
				cost := inputCost(change)
				if cost < 0 || cost > limits.Bytes-bytes {
					return Delta{}, fmt.Errorf("catalog input batch budget exceeded")
				}
				bytes += cost
			}
			seen[p.Key] = true
			inputs += len(p.Inputs)
			edges += len(p.Inputs)
			dependencies += p.MaxDependencies
			d.Upsert = append(d.Upsert, p)
		}
	}
	return d, nil
}
