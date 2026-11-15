document.addEventListener('alpine:init', () => {
    Alpine.data('partLinksForm', () => ({
        newLinks: [],
        addLink() { this.newLinks.push({url: '', label: ''}); },
        removeLink(index) { this.newLinks.splice(index, 1); }
    }));
});

(function() {
    let activeParseSeq = 0;
    let lastParsedRaw = '';
    let applyingAutofill = false;

    function getFormElements() {
        const form = document.getElementById('part-form');
        const byIdOrName = (id, name) =>
            document.getElementById(id) ||
            (form ? form.querySelector(`[name="${name}"]`) : document.querySelector(`[name="${name}"]`));

        return {
            form,
            nameInput: byIdOrName('name', 'name'),
            mpnInput: byIdOrName('part_number', 'part_number'),
            manufacturerInput: byIdOrName('manufacturer', 'manufacturer'),
            supplierInput: byIdOrName('supplier', 'supplier'),
            barcodeInput: byIdOrName('barcode_data', 'barcode_data'),
            parseBtn: document.getElementById('parse-barcode-btn'),
            banner: document.getElementById('barcode-parse-banner')
        };
    }

    function looksLike2DSupplierLabel(raw) {
        if (!raw) return false;
        const s = raw.trim();
        return (
            s.startsWith('[)>') ||
            s.startsWith('06\x1d') ||
            s.startsWith('{') ||
            s.includes('\x1d') ||
            s.includes('\x1e') ||
            /<(?:RS|GS|EOT)>/i.test(s) ||
            /(?:^|[,;])\s*pc\s*:/i.test(s)
        );
    }

    function applyFieldValue(input, nextVal, forceOverwrite) {
        if (!input || !nextVal) {
            return false;
        }
        const val = String(nextVal).trim();
        if (!val) {
            return false;
        }
        const current = (input.value || '').trim();
        const canOverwrite = current === '' || forceOverwrite || input.dataset.autofilled === 'true';
        if (!canOverwrite) {
            return false;
        }
        if (current === val) {
            return false;
        }

        applyingAutofill = true;
        try {
            input.value = val;
            input.dataset.autofilled = 'true';
            input.dispatchEvent(new Event('input', { bubbles: true }));
        } finally {
            applyingAutofill = false;
        }
        return true;
    }

    function buildMetadataSummary(data) {
        const parts = [];
        if (data.supplier) parts.push(data.supplier);
        if (data.part_number) parts.push(`MPN: ${data.part_number}`);
        if (data.supplier_part_number && data.supplier_part_number !== data.part_number) {
            parts.push(`SKU: ${data.supplier_part_number}`);
        }
        if (data.manufacturer) parts.push(`Mfr: ${data.manufacturer}`);
        if (data.quantity > 0) parts.push(`Qty: ${data.quantity}`);
        if (data.order_number) parts.push(`Order: ${data.order_number}`);
        return parts.join(' • ');
    }

    function clearBanner(banner) {
        if (!banner) return;
        banner.replaceChildren();
        banner.classList.add('hidden');
    }

    function renderMatchBanner(banner, data, isSelfEdit) {
        if (!banner) return;
        banner.replaceChildren();

        const summary = buildMetadataSummary(data);

        if (data.existing_part_id != null && !isSelfEdit) {
            const alertBox = document.createElement('div');
            alertBox.setAttribute('role', 'status');
            alertBox.className = 'alert alert-warning text-sm flex items-center justify-between gap-2';

            const textWrap = document.createElement('div');
            const label = document.createElement('span');
            label.textContent = 'Existing part matched: ';
            const strong = document.createElement('strong');
            strong.textContent = data.existing_part_name || `Part #${data.existing_part_id}`;
            textWrap.appendChild(label);
            textWrap.appendChild(strong);
            if (summary) {
                const metaSpan = document.createElement('span');
                metaSpan.textContent = ` (${summary})`;
                textWrap.appendChild(metaSpan);
            }

            const link = document.createElement('a');
            link.href = `/parts/${encodeURIComponent(data.existing_part_id)}`;
            link.className = 'btn btn-sm btn-outline';
            link.textContent = 'View Part';

            alertBox.appendChild(textWrap);
            alertBox.appendChild(link);
            banner.appendChild(alertBox);
            banner.classList.remove('hidden');
            return;
        }

        if (data.format && data.format !== '1D' && data.format !== 'UNKNOWN' && summary) {
            const alertBox = document.createElement('div');
            alertBox.setAttribute('role', 'status');
            alertBox.className = 'alert alert-info text-sm flex items-center justify-between gap-2';

            const span = document.createElement('span');
            span.textContent = `Parsed ${data.format}: ${summary}`;

            const dismissBtn = document.createElement('button');
            dismissBtn.type = 'button';
            dismissBtn.className = 'btn btn-ghost btn-xs';
            dismissBtn.textContent = '✕';
            dismissBtn.addEventListener('click', () => clearBanner(banner));

            alertBox.appendChild(span);
            alertBox.appendChild(dismissBtn);
            banner.appendChild(alertBox);
            banner.classList.remove('hidden');
            return;
        }

        clearBanner(banner);
    }

    function notifyUser(data, isSelfEdit) {
        if (!window.showToast) return;

        const summary = buildMetadataSummary(data);

        if (data.existing_part_id != null && !isSelfEdit) {
            const name = data.existing_part_name || `#${data.existing_part_id}`;
            const msg = summary
                ? `Existing part matched: "${name}" (#${data.existing_part_id}) — ${summary}`
                : `Existing part matched: "${name}" (#${data.existing_part_id})`;
            window.showToast(msg, 'alert-warning');
            return;
        }

        if (data.format && data.format !== '1D' && data.format !== 'UNKNOWN') {
            const label = data.supplier || data.format;
            const msg = summary
                ? `Parsed ${label} label: ${summary}`
                : `Parsed ${label} label`;
            window.showToast(msg, 'alert-success');
            return;
        }

        if (data.format === '1D' && data.raw_barcode) {
            window.showToast(`Scanned 1D barcode: ${data.raw_barcode}`, 'alert-info');
            return;
        }

        window.showToast('Unrecognized 2D format; raw barcode kept.', 'alert-info');
    }

    async function parseAndAutoFillBarcode(rawBarcode, options = {}) {
        const raw = typeof rawBarcode === 'string' ? rawBarcode : '';
        const trimmed = raw.trim();
        const {
            form,
            nameInput,
            mpnInput,
            manufacturerInput,
            supplierInput,
            barcodeInput,
            parseBtn,
            banner
        } = getFormElements();

        if (!trimmed) {
            clearBanner(banner);
            if (options.emptyToast && window.showToast) {
                window.showToast('Enter or paste a barcode label first.', 'alert-info');
            }
            if (options.emptyToast && barcodeInput) {
                barcodeInput.focus();
            }
            return null;
        }

        if (barcodeInput) {
            barcodeInput.dataset.rawBarcode = raw;
            if (!barcodeInput.value.trim() || options.fromScan) {
                barcodeInput.value = raw;
            }
        }

        const seq = ++activeParseSeq;
        lastParsedRaw = raw;
        if (parseBtn) {
            parseBtn.disabled = true;
            parseBtn.setAttribute('aria-busy', 'true');
        }

        try {
            const resp = await fetch('/parts/parse-barcode', {
                method: 'POST',
                credentials: 'same-origin',
                headers: {
                    'Content-Type': 'application/json',
                    'Accept': 'application/json'
                },
                body: JSON.stringify({ barcode: raw })
            });

            if (seq !== activeParseSeq) {
                return null;
            }

            if (!resp.ok) {
                lastParsedRaw = '';
                let errMessage = 'Failed to parse barcode label.';
                try {
                    const errBody = await resp.json();
                    if (errBody && errBody.error) {
                        errMessage = errBody.error;
                    }
                } catch (_) {
                    // Ignore non-JSON error body
                }
                if (window.showToast) {
                    window.showToast(errMessage, 'alert-error');
                }
                return null;
            }

            const data = await resp.json();
            if (seq !== activeParseSeq) {
                return null;
            }

            if (barcodeInput && data.raw_barcode) {
                barcodeInput.dataset.rawBarcode = data.raw_barcode;
                if (!barcodeInput.value.trim() || options.fromScan) {
                    barcodeInput.value = data.raw_barcode;
                }
            }

            const forceOverwrite = Boolean(options.forceOverwrite);
            applyFieldValue(mpnInput, data.part_number, forceOverwrite);
            applyFieldValue(manufacturerInput, data.manufacturer, forceOverwrite);
            applyFieldValue(supplierInput, data.supplier, forceOverwrite);

            const defaultName = data.part_number || data.supplier_part_number || '';
            if (defaultName) {
                applyFieldValue(nameInput, defaultName, forceOverwrite);
            }

            const actionAttr = form ? (form.getAttribute('action') || '') : '';
            const isSelfEdit = data.existing_part_id != null &&
                actionAttr.endsWith(`/parts/${data.existing_part_id}/update`);

            renderMatchBanner(banner, data, isSelfEdit);
            notifyUser(data, isSelfEdit);
            return data;
        } catch (err) {
            if (seq === activeParseSeq) {
                lastParsedRaw = '';
                if (window.showToast) {
                    window.showToast('Unable to reach barcode parser endpoint.', 'alert-error');
                }
            }
            return null;
        } finally {
            if (parseBtn && seq === activeParseSeq) {
                parseBtn.disabled = false;
                parseBtn.removeAttribute('aria-busy');
            }
        }
    }

    function initPartBarcodeAutoFill() {
        const {
            form,
            nameInput,
            mpnInput,
            manufacturerInput,
            supplierInput,
            barcodeInput,
            parseBtn,
            banner
        } = getFormElements();

        if (!barcodeInput || barcodeInput.dataset.barcodeAutofillBound === 'true') {
            return;
        }
        barcodeInput.dataset.barcodeAutofillBound = 'true';

        // Clear autofilled marker whenever the user manually edits an auto-filled field.
        [nameInput, mpnInput, manufacturerInput, supplierInput].forEach((field) => {
            if (!field) return;
            field.addEventListener('input', () => {
                if (!applyingAutofill) {
                    delete field.dataset.autofilled;
                }
            });
        });

        barcodeInput.addEventListener('input', () => {
            delete barcodeInput.dataset.rawBarcode;
            delete barcodeInput.dataset.rawScannedBarcode;
        });

        barcodeInput.addEventListener('paste', (event) => {
            const clipboard = event.clipboardData || window.clipboardData;
            const pasted = clipboard ? clipboard.getData('text') : '';
            if (!pasted || !pasted.trim()) {
                return;
            }
            barcodeInput.dataset.rawBarcode = pasted;
            setTimeout(() => {
                parseAndAutoFillBarcode(pasted, { forceTrigger: true });
            }, 0);
        });

        barcodeInput.addEventListener('change', () => {
            if (barcodeInput.dataset.barcodeScanning === '1') {
                return;
            }
            const raw = barcodeInput.dataset.rawBarcode || barcodeInput.value;
            if (!raw || !raw.trim()) {
                lastParsedRaw = '';
                clearBanner(banner);
                return;
            }
            if (raw !== lastParsedRaw) {
                parseAndAutoFillBarcode(raw, { forceTrigger: false });
            }
        });

        barcodeInput.addEventListener('keydown', (event) => {
            if (event.key !== 'Enter') {
                return;
            }
            const raw = barcodeInput.dataset.rawBarcode || barcodeInput.value || '';
            if (!raw.trim()) {
                return;
            }
            if (looksLike2DSupplierLabel(raw) || (form && !form.checkValidity())) {
                event.preventDefault();
                parseAndAutoFillBarcode(raw, { forceTrigger: true });
            }
        });

        if (parseBtn) {
            parseBtn.addEventListener('click', () => {
                const raw = barcodeInput.dataset.rawBarcode || barcodeInput.value;
                parseAndAutoFillBarcode(raw, { forceTrigger: true, emptyToast: true });
            });
        }

        const docHost = document.body || document.documentElement;
        if (docHost && !docHost.dataset.partFormBarcodeScannedBound) {
            document.addEventListener('barcode-scanned', (event) => {
                const detail = event && event.detail ? event.detail : {};
                const current = getFormElements();
                if (!current.barcodeInput) {
                    return;
                }
                if (detail.targetInputId && detail.targetInputId !== current.barcodeInput.id) {
                    return;
                }
                const raw = typeof detail.barcode === 'string' && detail.barcode !== ''
                    ? detail.barcode
                    : (current.barcodeInput.dataset.rawScannedBarcode || current.barcodeInput.value);
                if (!raw || !raw.trim()) {
                    return;
                }
                parseAndAutoFillBarcode(raw, { fromScan: true, forceTrigger: true });
            });
            docHost.dataset.partFormBarcodeScannedBound = 'true';
        }
    }

    window.parseAndAutoFillBarcode = parseAndAutoFillBarcode;

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initPartBarcodeAutoFill);
    } else {
        initPartBarcodeAutoFill();
    }
})();
