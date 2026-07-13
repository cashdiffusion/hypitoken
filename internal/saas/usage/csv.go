package usage

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// maxExportRows bounds a single export. A finance team wanting more than this
// wants a narrower date range, not a 200 MB download.
const maxExportRows = 200_000

// flushEvery keeps the client's progress bar moving on a big export without
// syscall-thrashing on a small one.
const flushEvery = 500

// csvHeaderPersonal / csvHeaderTeam differ only by the member column: on a
// personal export every row is by definition the caller, so naming them would be
// noise.
var (
	csvHeaderPersonal = []string{
		"time_utc", "time_local", "token_id", "token_name", "tags", "model",
		"amount_usd", "input_tokens", "output_tokens", "cache_read_tokens", "cache_create_tokens", "ref",
	}
	csvHeaderTeam = []string{
		"time_utc", "time_local", "member_email", "token_id", "token_name", "tags", "model",
		"amount_usd", "input_tokens", "output_tokens", "cache_read_tokens", "cache_create_tokens", "ref",
	}
)

// exportCSV streams the filtered charge ledger as a spreadsheet-ready CSV.
//
// Streamed rather than buffered: a 90-day enterprise export can run to six
// figures of rows, and materializing that would put a caller-controlled,
// multi-hundred-MB allocation in the hot path of a process that is simultaneously
// proxying LLM traffic.
func (h *Handler) exportCSV(c *gin.Context, f db.ReportFilter) {
	team := f.WorkspaceID > 0

	filename := exportFilename(f, team)
	// Content-Type must be set before the first byte goes out.
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	// The UTF-8 BOM is NOT optional. Excel on Windows — still the default tool for
	// a Chinese finance or HR team — decodes a BOM-less UTF-8 CSV as GBK, turning
	// every 研发部 into mojibake. Three bytes, and pandas / Numbers / LibreOffice /
	// Sheets all ignore it.
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	header := csvHeaderPersonal
	if team {
		header = csvHeaderTeam
	}
	if err := w.Write(header); err != nil {
		return // client hung up
	}

	n := 0
	truncated := false
	err := h.DB.StreamSpendRows(c.Request.Context(), f, func(r *db.SpendRow) error {
		if n >= maxExportRows {
			truncated = true
			return errTruncated
		}
		if err := w.Write(csvRow(r, team)); err != nil {
			return err
		}
		n++
		if n%flushEvery == 0 {
			w.Flush()
			c.Writer.Flush()
		}
		return nil
	})
	w.Flush()

	if truncated {
		// A visible comment beats a silently short file that under-reports spend.
		_, _ = fmt.Fprintf(c.Writer, "# truncated at %d rows — narrow the date range\n", maxExportRows)
	} else if err != nil {
		// Headers are already on the wire, so we can't switch to a JSON error.
		// Terminate the file with a marker the reader can actually see.
		_, _ = fmt.Fprintf(c.Writer, "# export failed: %v\n", err)
	}
	c.Writer.Flush()
}

// errTruncated stops the stream once the row cap is reached. It is a control
// signal, not a failure.
var errTruncated = fmt.Errorf("row cap reached")

func csvRow(r *db.SpendRow, team bool) []string {
	row := make([]string, 0, len(csvHeaderTeam))
	row = append(row,
		r.CreatedAt.UTC().Format(time.RFC3339),
		r.CreatedAt.Local().Format("2006-01-02 15:04:05"), // finance reconciles in local time
	)
	if team {
		row = append(row, sanitizeCell(r.Email))
	}
	row = append(row,
		tokenIDCell(r),
		sanitizeCell(tokenNameCell(r)),
		sanitizeCell(strings.Join(r.TokenTags, "|")), // "|" survives a naive re-split on ","
		sanitizeCell(r.Model),
		strconv.FormatFloat(r.AmountUSD, 'f', 6, 64), // charges are routinely sub-cent
		// Pre-v15 rows have no recorded token counts. Blank, not 0 — a reader must
		// be able to tell "no tokens" from "not recorded".
		countCell(r.InputTokens, r.Attributed),
		countCell(r.OutputTokens, r.Attributed),
		countCell(r.CacheReadTokens, r.Attributed),
		countCell(r.CacheCreateTokens, r.Attributed),
		sanitizeCell(r.Ref),
	)
	return row
}

func tokenIDCell(r *db.SpendRow) string {
	if r.TokenID == 0 {
		return ""
	}
	return strconv.FormatInt(r.TokenID, 10)
}

func tokenNameCell(r *db.SpendRow) string {
	if r.TokenName != "" {
		return r.TokenName
	}
	if r.TokenID == 0 {
		return "(unattributed)"
	}
	return fmt.Sprintf("(deleted #%d)", r.TokenID)
}

func countCell(v int64, attributed bool) string {
	if !attributed {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

// sanitizeCell defuses CSV formula injection. Key names and tags are authored by
// users and land in Excel, where a cell starting with =, +, -, @, TAB or CR is
// evaluated as a formula — a key named `=cmd|'/c calc'!A0` is a real (if
// low-severity) attack on whoever opens the export.
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// exportFilename builds a stable, sortable name. The workspace NAME is
// deliberately not interpolated: it is user-controlled text and would be flowing
// straight into a response header.
func exportFilename(f db.ReportFilter, team bool) string {
	scope := "me"
	if team {
		scope = "ws" + strconv.FormatInt(f.WorkspaceID, 10)
	}
	return fmt.Sprintf("usage-%s-%s-%s.csv", scope,
		f.From.Format("20060102"), f.To.AddDate(0, 0, -1).Format("20060102"))
}
