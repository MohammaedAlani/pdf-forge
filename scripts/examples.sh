#!/bin/bash

# ==============================================================================
# PDF Forge - Example Client Script
# Demonstrates all conversion capabilities
# ==============================================================================

set -e

API_URL="${PDF_FORGE_URL:-http://localhost:8080}"
API_KEY="${PDF_FORGE_API_KEY:-}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Helper function for API calls
api_call() {
    local endpoint=$1
    local data=$2
    local output=$3
    
    local headers=(-H "Content-Type: application/json")
    if [ -n "$API_KEY" ]; then
        headers+=(-H "X-API-Key: $API_KEY")
    fi
    
    echo -e "${BLUE}→ POST $API_URL$endpoint${NC}"
    
    if curl -s -f -X POST "$API_URL$endpoint" \
        "${headers[@]}" \
        -d "$data" \
        -o "$output"; then
        echo -e "${GREEN}✓ Created: $output${NC}"
        return 0
    else
        echo -e "${RED}✗ Failed${NC}"
        return 1
    fi
}

# ==============================================================================
# Examples
# ==============================================================================

echo "======================================"
echo "PDF Forge - Example Conversions"
echo "======================================"
echo ""

# 1. Simple HTML to PDF
echo "1. Simple HTML to PDF"
echo "---------------------"
api_call "/html" '{
    "html": "<!DOCTYPE html><html><head><style>body{font-family:Arial,sans-serif;padding:40px;}h1{color:#333;}p{color:#666;line-height:1.6;}</style></head><body><h1>Hello, PDF Forge!</h1><p>This is a simple HTML to PDF conversion example.</p><ul><li>Feature 1</li><li>Feature 2</li><li>Feature 3</li></ul></body></html>"
}' "01-simple.pdf"
echo ""

# 2. HTML with Password Protection
echo "2. Password Protected PDF"
echo "-------------------------"
api_call "/convert" '{
    "type": "html",
    "html": "<!DOCTYPE html><html><body><h1>🔒 Confidential Document</h1><p>This PDF requires a password to open.</p><p>Password: <code>secret123</code></p></body></html>",
    "options": {
        "security": {
            "user_password": "secret123",
            "owner_password": "admin456",
            "allow_printing": true,
            "allow_copying": false,
            "encryption_bits": 256
        },
        "metadata": {
            "title": "Confidential Report",
            "author": "PDF Forge"
        }
    }
}' "02-protected.pdf"
echo ""

# 3. Custom Page Size and Margins
echo "3. Custom Page Size (Letter, Landscape)"
echo "---------------------------------------"
api_call "/convert" '{
    "type": "html",
    "html": "<!DOCTYPE html><html><body style=\"background:#f0f0f0;padding:20px;\"><h1>Landscape Document</h1><p>This is a Letter-sized document in landscape orientation with custom margins.</p><table border=\"1\" style=\"width:100%;border-collapse:collapse;\"><tr><th>Column 1</th><th>Column 2</th><th>Column 3</th><th>Column 4</th></tr><tr><td>Data</td><td>Data</td><td>Data</td><td>Data</td></tr></table></body></html>",
    "options": {
        "page_size": "Letter",
        "orientation": "landscape",
        "margins": {
            "top": 1.0,
            "bottom": 1.0,
            "left": 0.5,
            "right": 0.5
        },
        "print_background": true
    }
}' "03-landscape.pdf"
echo ""

# 4. Markdown to PDF
echo "4. Markdown to PDF"
echo "------------------"
api_call "/markdown" '{
    "markdown": "# Project Documentation\n\n## Overview\n\nThis document demonstrates **Markdown to PDF** conversion.\n\n### Features\n\n- Automatic styling\n- Code blocks\n- Lists and tables\n\n### Code Example\n\n```go\nfunc main() {\n    fmt.Println(\"Hello, World!\")\n}\n```\n\n### Table\n\n| Feature | Status |\n|---------|--------|\n| HTML | ✅ |\n| Markdown | ✅ |\n| Images | ✅ |",
    "options": {
        "page_size": "A4",
        "metadata": {
            "title": "Project Documentation",
            "author": "Dev Team"
        }
    }
}' "04-markdown.pdf"
echo ""

# 5. Header and Footer
echo "5. PDF with Header and Footer"
echo "-----------------------------"
api_call "/convert" '{
    "type": "html",
    "html": "<!DOCTYPE html><html><body><h1>Document with Header/Footer</h1><p>This document has custom headers and footers on each page.</p><p style=\"page-break-after:always;\">Page 1 content here.</p><p>Page 2 content here.</p></body></html>",
    "options": {
        "header_footer": {
            "header_left": "PDF Forge Demo",
            "header_center": "",
            "header_right": "Confidential",
            "footer_left": "Generated: 2024",
            "footer_center": "Page <span class=\"pageNumber\"></span> of <span class=\"totalPages\"></span>",
            "footer_right": "",
            "font_size": 9
        },
        "margins": {
            "top": 0.75,
            "bottom": 0.75,
            "left": 0.5,
            "right": 0.5
        }
    }
}' "05-header-footer.pdf"
echo ""

# 6. Invoice Template
echo "6. Professional Invoice"
echo "-----------------------"
api_call "/html" '{
    "html": "<!DOCTYPE html><html><head><style>body{font-family:Helvetica,Arial,sans-serif;margin:0;padding:40px;color:#333}h1{color:#2c3e50;border-bottom:3px solid #3498db;padding-bottom:10px}.header{display:flex;justify-content:space-between;margin-bottom:30px}.company{font-size:24px;font-weight:bold;color:#2c3e50}.invoice-details{text-align:right}table{width:100%;border-collapse:collapse;margin:20px 0}th{background:#3498db;color:white;padding:12px;text-align:left}td{padding:12px;border-bottom:1px solid #ddd}.total{font-size:18px;font-weight:bold;text-align:right;margin-top:20px}.footer{margin-top:40px;text-align:center;color:#7f8c8d;font-size:12px}</style></head><body><div class=\"header\"><div class=\"company\">ACME Corp</div><div class=\"invoice-details\"><strong>INVOICE #001</strong><br>Date: Dec 1, 2024<br>Due: Dec 31, 2024</div></div><h1>Invoice</h1><p><strong>Bill To:</strong><br>Client Company<br>123 Business St<br>City, State 12345</p><table><tr><th>Description</th><th>Qty</th><th>Price</th><th>Total</th></tr><tr><td>PDF Conversion Service</td><td>100</td><td>$0.10</td><td>$10.00</td></tr><tr><td>Premium Support</td><td>1</td><td>$50.00</td><td>$50.00</td></tr><tr><td>API Integration</td><td>1</td><td>$200.00</td><td>$200.00</td></tr></table><div class=\"total\">Total: $260.00</div><div class=\"footer\">Thank you for your business!<br>Payment due within 30 days.</div></body></html>",
    "options": {
        "page_size": "A4",
        "print_background": true
    }
}' "06-invoice.pdf"
echo ""

# 7. URL to PDF (if network is available)
echo "7. URL to PDF"
echo "-------------"
api_call "/url" '{
    "url": "https://example.com",
    "options": {
        "page_size": "A4",
        "print_background": true
    }
}' "07-webpage.pdf" || echo "URL conversion may require network access"
echo ""

# Health Check
echo "======================================"
echo "Service Health Check"
echo "======================================"
echo ""
curl -s "$API_URL/health" | python3 -m json.tool 2>/dev/null || curl -s "$API_URL/health"
echo ""
echo ""

# Summary
echo "======================================"
echo "Generated PDFs:"
echo "======================================"
ls -lh *.pdf 2>/dev/null || echo "No PDFs found in current directory"
echo ""
echo -e "${GREEN}Done!${NC} Check the generated PDF files."
