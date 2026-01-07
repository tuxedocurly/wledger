function openAuditLogDetail(id, oldVal, newVal) {
    window.dispatchEvent(new CustomEvent('open-audit-detail', { 
        detail: { id, oldVal, newVal } 
    }));
}

function copyToClipboard(id) {
    const text = document.getElementById(id).textContent;
    navigator.clipboard.writeText(text);
}

document.addEventListener('DOMContentLoaded', () => {
    window.addEventListener('open-audit-detail', (e) => {
        const { id, oldVal, newVal } = e.detail;
        
        const formatJson = (val) => {
            if (!val || val === "null" || val === "{}") return "{}";
            try {
                const obj = JSON.parse(val);
                return JSON.stringify(obj, null, 2);
            } catch(e) {
                return val;
            }
        };

        const oldEl = document.getElementById('audit-detail-old');
        const newEl = document.getElementById('audit-detail-new');
        
        if (oldEl) oldEl.textContent = formatJson(oldVal);
        if (newEl) newEl.textContent = formatJson(newVal);
        
        const modal = document.getElementById('audit_detail_modal');
        if (modal) modal.showModal();
    });
});
