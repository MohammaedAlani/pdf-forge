package templates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// TemplateType defines available template types
type TemplateType string

const (
	TemplateInvoice     TemplateType = "invoice"
	TemplateReceipt     TemplateType = "receipt"
	TemplateCertificate TemplateType = "certificate"
	TemplateReport      TemplateType = "report"
	TemplateContract    TemplateType = "contract"
	TemplateCustom      TemplateType = "custom"
)

// TemplateEngine handles template rendering
type TemplateEngine struct {
	templates map[TemplateType]*template.Template
	funcMap   template.FuncMap
}

// NewTemplateEngine creates a new template engine
func NewTemplateEngine() *TemplateEngine {
	funcMap := template.FuncMap{
		"formatDate": func(t time.Time, layout string) string {
			if layout == "" {
				layout = "January 2, 2006"
			}
			return t.Format(layout)
		},
		"formatMoney": func(amount float64, currency string) string {
			if currency == "" {
				currency = "$"
			}
			return fmt.Sprintf("%s%.2f", currency, amount)
		},
		"add": func(a, b float64) float64 {
			return a + b
		},
		"subtract": func(a, b float64) float64 {
			return a - b
		},
		"multiply": func(a, b float64) float64 {
			return a * b
		},
		"divide": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"percentage": func(amount, total float64) string {
			if total == 0 {
				return "0%"
			}
			return fmt.Sprintf("%.1f%%", (amount/total)*100)
		},
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": strings.Title,
		"now":   time.Now,
		"seq": func(start, end int) []int {
			var result []int
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
			return result
		},
	}

	engine := &TemplateEngine{
		templates: make(map[TemplateType]*template.Template),
		funcMap:   funcMap,
	}

	// Register built-in templates
	engine.registerBuiltinTemplates()

	return engine
}

// Render renders a template with the given data
func (e *TemplateEngine) Render(templateType TemplateType, data map[string]interface{}) (string, error) {
	tmpl, ok := e.templates[templateType]
	if !ok {
		return "", fmt.Errorf("template not found: %s", templateType)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// RenderCustom renders a custom template string
func (e *TemplateEngine) RenderCustom(templateStr string, data map[string]interface{}) (string, error) {
	tmpl, err := template.New("custom").Funcs(e.funcMap).Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// RenderJSON renders a template with JSON data
func (e *TemplateEngine) RenderJSON(templateType TemplateType, jsonData string) (string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return "", fmt.Errorf("invalid JSON data: %w", err)
	}

	return e.Render(templateType, data)
}

func (e *TemplateEngine) registerBuiltinTemplates() {
	// Invoice Template
	e.templates[TemplateInvoice] = template.Must(template.New("invoice").Funcs(e.funcMap).Parse(invoiceTemplate))

	// Receipt Template
	e.templates[TemplateReceipt] = template.Must(template.New("receipt").Funcs(e.funcMap).Parse(receiptTemplate))

	// Certificate Template
	e.templates[TemplateCertificate] = template.Must(template.New("certificate").Funcs(e.funcMap).Parse(certificateTemplate))

	// Report Template
	e.templates[TemplateReport] = template.Must(template.New("report").Funcs(e.funcMap).Parse(reportTemplate))

	// Contract Template
	e.templates[TemplateContract] = template.Must(template.New("contract").Funcs(e.funcMap).Parse(contractTemplate))
}

// Built-in Templates

const invoiceTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: 'Helvetica Neue', Arial, sans-serif; color: #333; padding: 40px; }
        .header { display: flex; justify-content: space-between; margin-bottom: 40px; }
        .company-info h1 { font-size: 28px; color: {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; margin-bottom: 5px; }
        .company-info p { color: #666; font-size: 14px; }
        .invoice-details { text-align: right; }
        .invoice-details h2 { font-size: 32px; color: #333; margin-bottom: 10px; }
        .invoice-details p { font-size: 14px; color: #666; }
        .addresses { display: flex; justify-content: space-between; margin-bottom: 40px; }
        .address-block { width: 45%; }
        .address-block h3 { font-size: 12px; color: #999; text-transform: uppercase; margin-bottom: 10px; }
        .address-block p { font-size: 14px; line-height: 1.6; }
        table { width: 100%; border-collapse: collapse; margin-bottom: 30px; }
        th { background: {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; color: white; padding: 12px 15px; text-align: left; font-size: 12px; text-transform: uppercase; }
        td { padding: 15px; border-bottom: 1px solid #eee; font-size: 14px; }
        tr:nth-child(even) { background: #f9fafb; }
        .amount { text-align: right; }
        .totals { width: 300px; margin-left: auto; }
        .totals tr td { border: none; padding: 8px 15px; }
        .totals .label { text-align: right; color: #666; }
        .totals .value { text-align: right; font-weight: 500; }
        .totals .total-row td { font-size: 18px; font-weight: bold; border-top: 2px solid #333; padding-top: 15px; }
        .totals .total-row .value { color: {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; }
        .footer { margin-top: 60px; padding-top: 20px; border-top: 1px solid #eee; }
        .footer p { font-size: 12px; color: #999; text-align: center; }
        .notes { background: #f9fafb; padding: 20px; border-radius: 8px; margin-top: 30px; }
        .notes h4 { font-size: 14px; margin-bottom: 10px; }
        .notes p { font-size: 13px; color: #666; }
        .status { display: inline-block; padding: 5px 12px; border-radius: 20px; font-size: 12px; font-weight: 600; }
        .status-paid { background: #dcfce7; color: #16a34a; }
        .status-pending { background: #fef3c7; color: #d97706; }
        .status-overdue { background: #fee2e2; color: #dc2626; }
    </style>
</head>
<body>
    <div class="header">
        <div class="company-info">
            <h1>{{.company_name}}</h1>
            <p>{{.company_address}}</p>
            <p>{{.company_email}} | {{.company_phone}}</p>
        </div>
        <div class="invoice-details">
            <h2>INVOICE</h2>
            <p><strong>#{{.invoice_number}}</strong></p>
            <p>Date: {{if .invoice_date}}{{.invoice_date}}{{else}}{{formatDate now ""}}{{end}}</p>
            <p>Due: {{.due_date}}</p>
            {{if .status}}<p><span class="status status-{{.status | lower}}">{{.status | upper}}</span></p>{{end}}
        </div>
    </div>

    <div class="addresses">
        <div class="address-block">
            <h3>Bill To</h3>
            <p><strong>{{.client_name}}</strong></p>
            <p>{{.client_address}}</p>
            <p>{{.client_email}}</p>
        </div>
        {{if .ship_to}}
        <div class="address-block">
            <h3>Ship To</h3>
            <p>{{.ship_to}}</p>
        </div>
        {{end}}
    </div>

    <table>
        <thead>
            <tr>
                <th>Description</th>
                <th>Qty</th>
                <th>Unit Price</th>
                <th class="amount">Amount</th>
            </tr>
        </thead>
        <tbody>
            {{range .items}}
            <tr>
                <td>{{.description}}</td>
                <td>{{.quantity}}</td>
                <td>{{formatMoney .unit_price $.currency}}</td>
                <td class="amount">{{formatMoney .amount $.currency}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>

    <table class="totals">
        <tr>
            <td class="label">Subtotal</td>
            <td class="value">{{formatMoney .subtotal .currency}}</td>
        </tr>
        {{if .discount}}
        <tr>
            <td class="label">Discount</td>
            <td class="value">-{{formatMoney .discount .currency}}</td>
        </tr>
        {{end}}
        {{if .tax}}
        <tr>
            <td class="label">Tax ({{.tax_rate}}%)</td>
            <td class="value">{{formatMoney .tax .currency}}</td>
        </tr>
        {{end}}
        <tr class="total-row">
            <td class="label">Total</td>
            <td class="value">{{formatMoney .total .currency}}</td>
        </tr>
    </table>

    {{if .notes}}
    <div class="notes">
        <h4>Notes</h4>
        <p>{{.notes}}</p>
    </div>
    {{end}}

    {{if .payment_terms}}
    <div class="notes">
        <h4>Payment Terms</h4>
        <p>{{.payment_terms}}</p>
    </div>
    {{end}}

    <div class="footer">
        <p>Thank you for your business!</p>
    </div>
</body>
</html>`

const receiptTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: 'Courier New', monospace; max-width: 400px; margin: 0 auto; padding: 20px; }
        .header { text-align: center; border-bottom: 2px dashed #333; padding-bottom: 20px; margin-bottom: 20px; }
        .header h1 { font-size: 24px; margin-bottom: 5px; }
        .header p { font-size: 12px; color: #666; }
        .transaction { margin-bottom: 20px; }
        .transaction p { font-size: 12px; margin: 5px 0; }
        .items { border-top: 1px dashed #333; border-bottom: 1px dashed #333; padding: 15px 0; }
        .item { display: flex; justify-content: space-between; margin: 8px 0; font-size: 14px; }
        .totals { padding: 15px 0; }
        .total-line { display: flex; justify-content: space-between; margin: 5px 0; font-size: 14px; }
        .grand-total { font-size: 18px; font-weight: bold; border-top: 2px solid #333; padding-top: 10px; margin-top: 10px; }
        .footer { text-align: center; margin-top: 30px; font-size: 12px; }
        .barcode { text-align: center; margin: 20px 0; font-family: 'Libre Barcode 39', cursive; font-size: 48px; }
    </style>
</head>
<body>
    <div class="header">
        <h1>{{.store_name}}</h1>
        <p>{{.store_address}}</p>
        <p>Tel: {{.store_phone}}</p>
    </div>

    <div class="transaction">
        <p>Receipt #: {{.receipt_number}}</p>
        <p>Date: {{if .date}}{{.date}}{{else}}{{formatDate now "01/02/2006 15:04"}}{{end}}</p>
        <p>Cashier: {{.cashier}}</p>
        {{if .customer}}<p>Customer: {{.customer}}</p>{{end}}
    </div>

    <div class="items">
        {{range .items}}
        <div class="item">
            <span>{{.name}} x{{.quantity}}</span>
            <span>{{formatMoney .total $.currency}}</span>
        </div>
        {{end}}
    </div>

    <div class="totals">
        <div class="total-line">
            <span>Subtotal</span>
            <span>{{formatMoney .subtotal .currency}}</span>
        </div>
        {{if .tax}}
        <div class="total-line">
            <span>Tax</span>
            <span>{{formatMoney .tax .currency}}</span>
        </div>
        {{end}}
        {{if .discount}}
        <div class="total-line">
            <span>Discount</span>
            <span>-{{formatMoney .discount .currency}}</span>
        </div>
        {{end}}
        <div class="total-line grand-total">
            <span>TOTAL</span>
            <span>{{formatMoney .total .currency}}</span>
        </div>
        <div class="total-line">
            <span>{{.payment_method}}</span>
            <span>{{formatMoney .amount_paid .currency}}</span>
        </div>
        {{if .change}}
        <div class="total-line">
            <span>Change</span>
            <span>{{formatMoney .change .currency}}</span>
        </div>
        {{end}}
    </div>

    <div class="footer">
        <p>{{if .footer_message}}{{.footer_message}}{{else}}Thank you for shopping with us!{{end}}</p>
        <p>{{if .return_policy}}{{.return_policy}}{{end}}</p>
    </div>
</body>
</html>`

const certificateTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        @page { size: landscape; margin: 0; }
        body { 
            font-family: 'Georgia', serif; 
            text-align: center; 
            padding: 50px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .certificate {
            background: white;
            padding: 60px 80px;
            border: 3px solid #d4af37;
            box-shadow: 0 0 0 10px white, 0 0 0 13px #d4af37;
            max-width: 900px;
        }
        .ornament { color: #d4af37; font-size: 36px; margin: 10px 0; }
        .title { 
            font-size: 48px; 
            color: #1a365d; 
            text-transform: uppercase; 
            letter-spacing: 8px;
            margin: 20px 0;
        }
        .subtitle { font-size: 18px; color: #666; margin: 10px 0; }
        .recipient { 
            font-size: 42px; 
            color: #2c5282; 
            font-style: italic;
            margin: 30px 0;
            border-bottom: 2px solid #d4af37;
            display: inline-block;
            padding: 10px 40px;
        }
        .description { font-size: 18px; color: #444; line-height: 1.8; margin: 30px 0; max-width: 600px; margin-left: auto; margin-right: auto; }
        .date { font-size: 16px; color: #666; margin: 30px 0; }
        .signatures { display: flex; justify-content: space-around; margin-top: 50px; }
        .signature { text-align: center; }
        .signature-line { width: 200px; border-top: 1px solid #333; margin-bottom: 10px; }
        .signature-name { font-size: 14px; font-weight: bold; }
        .signature-title { font-size: 12px; color: #666; }
        .seal { 
            width: 100px; 
            height: 100px; 
            border: 3px solid #d4af37;
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 20px auto;
            font-size: 12px;
            color: #d4af37;
            text-transform: uppercase;
        }
    </style>
</head>
<body>
    <div class="certificate">
        <div class="ornament">❧ ☙</div>
        <h1 class="title">{{if .title}}{{.title}}{{else}}Certificate{{end}}</h1>
        <p class="subtitle">{{if .subtitle}}{{.subtitle}}{{else}}of Achievement{{end}}</p>
        <div class="ornament">✦</div>
        
        <p class="subtitle">This is to certify that</p>
        <p class="recipient">{{.recipient_name}}</p>
        
        <p class="description">{{.description}}</p>
        
        <p class="date">
            {{if .date}}Awarded on {{.date}}{{else}}Awarded on {{formatDate now "January 2, 2006"}}{{end}}
            {{if .location}}<br>{{.location}}{{end}}
        </p>

        {{if .show_seal}}
        <div class="seal">Official<br>Seal</div>
        {{end}}

        <div class="signatures">
            {{range .signatures}}
            <div class="signature">
                <div class="signature-line"></div>
                <p class="signature-name">{{.name}}</p>
                <p class="signature-title">{{.title}}</p>
            </div>
            {{end}}
        </div>
        
        {{if .certificate_id}}
        <p style="font-size: 10px; color: #999; margin-top: 30px;">Certificate ID: {{.certificate_id}}</p>
        {{end}}
    </div>
</body>
</html>`

const reportTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: 'Helvetica Neue', Arial, sans-serif; color: #333; padding: 40px; line-height: 1.6; }
        .header { border-bottom: 3px solid {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; padding-bottom: 20px; margin-bottom: 30px; }
        .header h1 { font-size: 28px; margin-bottom: 5px; }
        .header .meta { color: #666; font-size: 14px; }
        .executive-summary { background: #f8fafc; padding: 20px; border-left: 4px solid {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; margin-bottom: 30px; }
        .executive-summary h2 { font-size: 16px; margin-bottom: 10px; }
        h2 { font-size: 20px; color: {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; margin-top: 30px; border-bottom: 1px solid #eee; padding-bottom: 10px; }
        h3 { font-size: 16px; margin-top: 20px; }
        p { margin: 10px 0; }
        table { width: 100%; border-collapse: collapse; margin: 20px 0; }
        th { background: #f1f5f9; padding: 12px; text-align: left; font-size: 12px; text-transform: uppercase; }
        td { padding: 12px; border-bottom: 1px solid #eee; }
        .metric-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; margin: 20px 0; }
        .metric { background: #f8fafc; padding: 20px; border-radius: 8px; text-align: center; }
        .metric-value { font-size: 32px; font-weight: bold; color: {{if .brand_color}}{{.brand_color}}{{else}}#2563eb{{end}}; }
        .metric-label { font-size: 12px; color: #666; text-transform: uppercase; }
        .chart-placeholder { background: #f1f5f9; height: 200px; display: flex; align-items: center; justify-content: center; color: #999; margin: 20px 0; border-radius: 8px; }
        .footer { margin-top: 50px; padding-top: 20px; border-top: 1px solid #eee; font-size: 12px; color: #666; }
        .page-break { page-break-after: always; }
        .toc { background: #f8fafc; padding: 20px; margin-bottom: 30px; }
        .toc h2 { margin-top: 0; }
        .toc ul { list-style: none; padding: 0; }
        .toc li { padding: 5px 0; border-bottom: 1px dotted #ddd; }
        .highlight { background: #fef3c7; padding: 2px 6px; border-radius: 3px; }
        .callout { background: #eff6ff; border: 1px solid #bfdbfe; padding: 15px; border-radius: 8px; margin: 20px 0; }
        .callout-warning { background: #fef3c7; border-color: #fcd34d; }
        .callout-success { background: #dcfce7; border-color: #86efac; }
    </style>
</head>
<body>
    <div class="header">
        <h1>{{.title}}</h1>
        <div class="meta">
            {{if .subtitle}}<p>{{.subtitle}}</p>{{end}}
            <p>Prepared by: {{.author}} | Date: {{if .date}}{{.date}}{{else}}{{formatDate now ""}}{{end}}</p>
            {{if .version}}<p>Version: {{.version}}</p>{{end}}
        </div>
    </div>

    {{if .executive_summary}}
    <div class="executive-summary">
        <h2>Executive Summary</h2>
        <p>{{.executive_summary}}</p>
    </div>
    {{end}}

    {{if .metrics}}
    <div class="metric-grid">
        {{range .metrics}}
        <div class="metric">
            <div class="metric-value">{{.value}}</div>
            <div class="metric-label">{{.label}}</div>
        </div>
        {{end}}
    </div>
    {{end}}

    {{range .sections}}
    <h2>{{.title}}</h2>
    {{if .content}}<p>{{.content}}</p>{{end}}
    
    {{if .subsections}}
    {{range .subsections}}
    <h3>{{.title}}</h3>
    <p>{{.content}}</p>
    {{end}}
    {{end}}

    {{if .table}}
    <table>
        <thead>
            <tr>
                {{range .table.headers}}
                <th>{{.}}</th>
                {{end}}
            </tr>
        </thead>
        <tbody>
            {{range .table.rows}}
            <tr>
                {{range .}}
                <td>{{.}}</td>
                {{end}}
            </tr>
            {{end}}
        </tbody>
    </table>
    {{end}}
    {{end}}

    {{if .conclusions}}
    <h2>Conclusions</h2>
    <p>{{.conclusions}}</p>
    {{end}}

    {{if .recommendations}}
    <h2>Recommendations</h2>
    <ul>
        {{range .recommendations}}
        <li>{{.}}</li>
        {{end}}
    </ul>
    {{end}}

    <div class="footer">
        <p>{{if .footer}}{{.footer}}{{else}}Confidential - For Internal Use Only{{end}}</p>
        <p>Generated by PDF Forge</p>
    </div>
</body>
</html>`

const contractTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: 'Times New Roman', serif; color: #333; padding: 50px; line-height: 1.8; font-size: 14px; }
        .header { text-align: center; margin-bottom: 40px; }
        .header h1 { font-size: 24px; text-transform: uppercase; letter-spacing: 2px; }
        .header p { color: #666; }
        .parties { margin-bottom: 30px; }
        .party { margin-bottom: 20px; }
        .party strong { display: block; margin-bottom: 5px; }
        .clause { margin-bottom: 25px; }
        .clause h3 { font-size: 14px; text-transform: uppercase; margin-bottom: 10px; }
        .clause p { text-align: justify; }
        .signatures { margin-top: 60px; display: flex; justify-content: space-between; }
        .signature-block { width: 45%; }
        .signature-line { border-top: 1px solid #333; margin-top: 60px; padding-top: 10px; }
        .signature-label { font-size: 12px; color: #666; }
        .footer { margin-top: 40px; text-align: center; font-size: 12px; color: #666; }
        .exhibit { page-break-before: always; }
        .exhibit h2 { text-align: center; margin-bottom: 30px; }
    </style>
</head>
<body>
    <div class="header">
        <h1>{{.title}}</h1>
        <p>Effective Date: {{if .effective_date}}{{.effective_date}}{{else}}{{formatDate now ""}}{{end}}</p>
    </div>

    <div class="parties">
        <p>This Agreement is entered into by and between:</p>
        {{range $i, $party := .parties}}
        <div class="party">
            <strong>{{if eq $i 0}}PARTY A{{else}}PARTY B{{end}} ("{{$party.short_name}}"):</strong>
            {{$party.full_name}}<br>
            {{$party.address}}<br>
            {{if $party.registration}}Registration: {{$party.registration}}{{end}}
        </div>
        {{end}}
    </div>

    <p><strong>WHEREAS</strong>, the parties wish to enter into this agreement under the following terms and conditions:</p>

    {{range $i, $clause := .clauses}}
    <div class="clause">
        <h3>{{add $i 1}}. {{$clause.title}}</h3>
        <p>{{$clause.content}}</p>
    </div>
    {{end}}

    {{if .governing_law}}
    <div class="clause">
        <h3>GOVERNING LAW</h3>
        <p>This Agreement shall be governed by and construed in accordance with the laws of {{.governing_law}}.</p>
    </div>
    {{end}}

    <p><strong>IN WITNESS WHEREOF</strong>, the parties have executed this Agreement as of the date first written above.</p>

    <div class="signatures">
        {{range $i, $party := .parties}}
        <div class="signature-block">
            <div class="signature-line">
                <p class="signature-label">Signature</p>
            </div>
            <p><strong>{{$party.short_name}}</strong></p>
            <p>Name: _______________________</p>
            <p>Title: _______________________</p>
            <p>Date: _______________________</p>
        </div>
        {{end}}
    </div>

    {{if .exhibits}}
    {{range $i, $exhibit := .exhibits}}
    <div class="exhibit">
        <h2>EXHIBIT {{add $i 1}}: {{$exhibit.title}}</h2>
        <p>{{$exhibit.content}}</p>
    </div>
    {{end}}
    {{end}}

    <div class="footer">
        {{if .contract_id}}<p>Contract ID: {{.contract_id}}</p>{{end}}
        <p>Page <span class="pageNumber"></span> of <span class="totalPages"></span></p>
    </div>
</body>
</html>`
