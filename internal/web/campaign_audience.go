package web

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// buildAudienceTx materializes a campaign audience into campaign_contacts.
// Only contacts with a deliverable-looking email are included; duplicates and
// suppressed/bounced addresses are skipped. Returns contacts added.
func buildAudienceTx(ctx context.Context, pool *pgxpool.Pool, tenantID, campaignID string) (int, error) {
	var audType string
	var audID *string
	if err := pool.QueryRow(ctx, `SELECT audience_type, audience_id::text FROM campaigns WHERE id=$1::uuid AND tenant_id=$2`,
		campaignID, tenantID).Scan(&audType, &audID); err != nil {
		return 0, err
	}
	if audID == nil || *audID == "" {
		return 0, nil
	}
	var leadIDs []string
	if audType == "list" {
		rows, err := pool.Query(ctx, `
			SELECT ll.lead_id::text FROM list_leads ll
			JOIN lists l ON l.id=ll.list_id
			WHERE ll.list_id=$1::uuid AND l.tenant_id=$2`, *audID, tenantID)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var lid string
			if err := rows.Scan(&lid); err == nil {
				leadIDs = append(leadIDs, lid)
			}
		}
	} else {
		rows, err := pool.Query(ctx, `
			SELECT sm.lead_id::text FROM segment_members sm
			JOIN segments sg ON sg.id=sm.segment_id
			WHERE sm.segment_id=$1::uuid AND sg.tenant_id=$2`, *audID, tenantID)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var lid string
			if err := rows.Scan(&lid); err == nil {
				leadIDs = append(leadIDs, lid)
			}
		}
	}
	if len(leadIDs) == 0 {
		return 0, nil
	}
	// primary contacts with email, excluding suppressed + bad statuses
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT l.primary_contact_id::text
		FROM leads l JOIN contacts ct ON ct.id = l.primary_contact_id
		WHERE l.tenant_id=$1 AND ct.tenant_id=$1 AND l.id = ANY($2::uuid[])
			AND ct.email <> '' AND ct.email_status NOT IN ('invalid','disposable')
			AND NOT EXISTS (SELECT 1 FROM suppression_list sl WHERE sl.tenant_id=$1 AND sl.email = ct.email)`,
		tenantID, leadIDs)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	added := 0
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			continue
		}
		// link back to a lead for context
		var leadID string
		_ = pool.QueryRow(ctx, `SELECT id::text FROM leads WHERE tenant_id=$1 AND primary_contact_id=$2::uuid LIMIT 1`,
			tenantID, cid).Scan(&leadID)
		var lid any
		if leadID != "" {
			lid = leadID
		}
		res, err := pool.Exec(ctx, `
			INSERT INTO campaign_contacts (tenant_id, campaign_id, contact_id, lead_id, status, next_send_at)
			VALUES ($1,$2::uuid,$3::uuid,$4::uuid,'pending',now()) ON CONFLICT (campaign_id, contact_id) DO NOTHING`,
			tenantID, campaignID, cid, lid)
		if err == nil {
			added += int(res.RowsAffected())
		}
	}
	return added, nil
}
