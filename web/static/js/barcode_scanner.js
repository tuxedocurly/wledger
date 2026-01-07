let html5QrcodeScanner;
let activeTargetInputId = null;

function startGlobalScanner(targetInputId) {
    // Security Check
    if (!window.isSecureContext) {
        alert(
            "Camera access is blocked because this page is not served over HTTPS.\n\n" +
            "To fix this:\n",
            "1. Use a Reverse Proxy with HTTPS (Caddy/Nginx)\n",
            "2. Or enable the 'Insecure origins' flag in your browser settings for this IP."
        );
        return;
    }

    activeTargetInputId = targetInputId;
    const modal = document.getElementById('global_scanner_modal');

    if (modal) {
        modal.showModal();

        setTimeout(() => {
            if (!html5QrcodeScanner) {
                html5QrcodeScanner = new Html5QrcodeScanner(
                    "reader",
                    {
                        fps: 10,
                        qrbox: { width: 250, height: 250 },
                        aspectRatio: 1.0,
                        showTorchButtonIfSupported: true
                    },
                    false
                );
            }
            html5QrcodeScanner.render(onGlobalScanSuccess, (errorMessage) => {
                // Standard scanning errors ignored
            });
        }, 100);
    }
}

function stopGlobalScanner() {
    const modal = document.getElementById('global_scanner_modal');

    if (html5QrcodeScanner) {
        try {
            html5QrcodeScanner.clear().catch(err => {
                console.warn("Scanner clear warning:", err);
            });
        } catch (e) {
            console.warn("Scanner clear error:", e);
        }
    }

    if (modal) {
        modal.close();
    }
    activeTargetInputId = null;
}

function onGlobalScanSuccess(decodedText, decodedResult) {
    if (activeTargetInputId) {
        const input = document.getElementById(activeTargetInputId);
        if (input) {
            input.value = decodedText;
            input.dispatchEvent(new Event('change'));
        }
    }
    stopGlobalScanner();
}
