function openAuditLogDetail(id, oldVal, newVal) {
    window.dispatchEvent(new CustomEvent('open-audit-detail', {
        detail: { id, oldVal, newVal }
    }));
}

function copyToClipboard(id) {
    const el = document.getElementById(id);
    if (!el) return Promise.resolve();
    const text = el.textContent || '';

    return window.copyTextSafe(text)
        .then(() => {
            if (window.showToast) {
                window.showToast('Copied to clipboard', 'alert-success');
            }
        })
        .catch(err => {
            console.error('Failed to copy audit payload:', err);
            if (window.showToast) {
                window.showToast('Failed to copy to clipboard', 'alert-error');
            }
        });
}

(function initAuditDetailModal() {
    if (window.__wledgerAuditDetailInit) return;
    window.__wledgerAuditDetailInit = true;

    const attach = () => {
        window.addEventListener('open-audit-detail', (e) => {
            const { oldVal, newVal } = e.detail;

            const formatJson = (str) => {
                if (!str || str === 'null' || str === '') return '// None';
                try {
                    const obj = JSON.parse(str);
                    return JSON.stringify(obj, null, 2);
                } catch (err) {
                    return str;
                }
            };

            const oldEl = document.getElementById('audit-detail-old');
            const newEl = document.getElementById('audit-detail-new');
            if (oldEl) oldEl.textContent = formatJson(oldVal);
            if (newEl) newEl.textContent = formatJson(newVal);

            const modal = document.getElementById('audit_detail_modal');
            if (modal) modal.showModal();
        });
    };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', attach);
    } else {
        attach();
    }
})();
