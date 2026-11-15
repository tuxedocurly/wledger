const DESIRED_BARCODE_FORMATS = [
    'data_matrix',
    'pdf417',
    'qr_code',
    'code_128',
    'ean_13',
    'upc_a'
];

let html5QrcodeScanner = null;
let nativeVideoStream = null;
let nativeLoopTimer = null;
let nativeRafId = null;
let activeTargetInputId = null;
let activeScanSessionId = 0;

async function createNativeBarcodeDetector() {
    if (
        typeof window.BarcodeDetector !== 'function' ||
        typeof navigator === 'undefined' ||
        !navigator.mediaDevices ||
        typeof navigator.mediaDevices.getUserMedia !== 'function'
    ) {
        return null;
    }

    try {
        if (typeof window.BarcodeDetector.getSupportedFormats === 'function') {
            const supported = await window.BarcodeDetector.getSupportedFormats();
            const formats = DESIRED_BARCODE_FORMATS.filter((fmt) => supported.includes(fmt));
            if (formats.length === 0) {
                return null;
            }
            return new window.BarcodeDetector({ formats });
        }
        return new window.BarcodeDetector({ formats: DESIRED_BARCODE_FORMATS });
    } catch (err) {
        console.warn('Native BarcodeDetector unavailable, falling back to Html5QrcodeScanner:', err);
        return null;
    }
}

function cleanupScannerResources() {
    activeScanSessionId++;

    if (nativeLoopTimer !== null) {
        clearTimeout(nativeLoopTimer);
        nativeLoopTimer = null;
    }
    if (nativeRafId !== null) {
        cancelAnimationFrame(nativeRafId);
        nativeRafId = null;
    }
    if (nativeVideoStream) {
        nativeVideoStream.getTracks().forEach((track) => {
            try {
                track.stop();
            } catch (_) {
                // Ignore track stop errors
            }
        });
        nativeVideoStream = null;
    }

    if (html5QrcodeScanner) {
        try {
            const clearResult = html5QrcodeScanner.clear();
            if (clearResult && typeof clearResult.catch === 'function') {
                clearResult.catch((err) => {
                    console.warn('Scanner clear warning:', err);
                });
            }
        } catch (err) {
            console.warn('Scanner clear error:', err);
        }
        html5QrcodeScanner = null;
    }

    const reader = document.getElementById('reader');
    if (reader) {
        reader.replaceChildren();
    }
}

async function startNativeBarcodeScanner(detector, sessionId) {
    const reader = document.getElementById('reader');
    if (!reader) {
        throw new Error('Scanner reader container not found');
    }

    const stream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: { ideal: 'environment' } },
        audio: false
    });

    if (sessionId !== activeScanSessionId) {
        stream.getTracks().forEach((track) => track.stop());
        return;
    }

    nativeVideoStream = stream;

    const video = document.createElement('video');
    video.playsInline = true;
    video.muted = true;
    video.autoplay = true;
    video.className = 'w-full h-full object-cover rounded-lg';
    video.srcObject = stream;

    reader.replaceChildren(video);
    await video.play();

    if (sessionId !== activeScanSessionId) {
        cleanupScannerResources();
        return;
    }

    const scanFrame = async () => {
        if (sessionId !== activeScanSessionId) {
            return;
        }
        try {
            if (video.readyState >= HTMLMediaElement.HAVE_ENOUGH_DATA && video.videoWidth > 0) {
                const barcodes = await detector.detect(video);
                if (sessionId !== activeScanSessionId) {
                    return;
                }
                if (Array.isArray(barcodes)) {
                    const hit = barcodes.find((code) => code && typeof code.rawValue === 'string' && code.rawValue.length > 0);
                    if (hit) {
                        onGlobalScanSuccess(hit.rawValue, hit);
                        return;
                    }
                }
            }
        } catch (err) {
            console.warn('Native BarcodeDetector frame error:', err);
        }

        if (sessionId === activeScanSessionId) {
            nativeLoopTimer = setTimeout(() => {
                if (sessionId === activeScanSessionId) {
                    nativeRafId = requestAnimationFrame(scanFrame);
                }
            }, 100);
        }
    };

    nativeRafId = requestAnimationFrame(scanFrame);
}

function startHtml5QrcodeFallback(sessionId) {
    setTimeout(() => {
        if (sessionId !== activeScanSessionId) {
            return;
        }
        if (typeof Html5QrcodeScanner === 'undefined') {
            console.error('Html5QrcodeScanner is not loaded');
            if (window.showToast) {
                window.showToast('Barcode scanner library failed to load.', 'alert-error');
            }
            return;
        }

        html5QrcodeScanner = new Html5QrcodeScanner(
            'reader',
            {
                fps: 10,
                qrbox: { width: 250, height: 250 },
                aspectRatio: 1.0,
                showTorchButtonIfSupported: true
            },
            false
        );
        html5QrcodeScanner.render(onGlobalScanSuccess, () => {
            // Standard scanning errors ignored
        });
    }, 100);
}

async function startGlobalScanner(targetInputId) {
    // Security Check
    if (!window.isSecureContext) {
        alert(
            "Camera access is blocked because this page is not served over HTTPS.\n\n" +
            "To fix this:\n" +
            "1. Use a Reverse Proxy with HTTPS (Caddy/Nginx)\n" +
            "2. Or enable the 'Insecure origins' flag in your browser settings for this IP."
        );
        return;
    }

    cleanupScannerResources();
    const sessionId = activeScanSessionId;
    activeTargetInputId = targetInputId || null;

    const modal = document.getElementById('global_scanner_modal');
    if (!modal) {
        return;
    }

    if (!modal.dataset.scannerCloseBound) {
        modal.addEventListener('close', () => {
            cleanupScannerResources();
            activeTargetInputId = null;
        });
        modal.dataset.scannerCloseBound = 'true';
    }

    modal.showModal();

    const detector = await createNativeBarcodeDetector();
    if (sessionId !== activeScanSessionId) {
        return;
    }

    if (detector) {
        try {
            await startNativeBarcodeScanner(detector, sessionId);
            return;
        } catch (err) {
            console.warn('Native BarcodeDetector failed, falling back to Html5QrcodeScanner:', err);
            if (sessionId !== activeScanSessionId) {
                return;
            }
            if (nativeVideoStream) {
                nativeVideoStream.getTracks().forEach((track) => track.stop());
                nativeVideoStream = null;
            }
            const reader = document.getElementById('reader');
            if (reader) {
                reader.replaceChildren();
            }
        }
    }

    startHtml5QrcodeFallback(sessionId);
}

function stopGlobalScanner() {
    cleanupScannerResources();

    const modal = document.getElementById('global_scanner_modal');
    if (modal && modal.open) {
        modal.close();
    }
    activeTargetInputId = null;
}

function onGlobalScanSuccess(decodedText, decodedResult) {
    const targetId = activeTargetInputId;
    const scanDetail = {
        barcode: decodedText,
        targetInputId: targetId,
        result: decodedResult || null
    };

    const targetInput = targetId ? document.getElementById(targetId) : null;
    if (targetInput) {
        targetInput.dataset.barcodeScanning = '1';
        targetInput.dataset.rawScannedBarcode = decodedText;
        targetInput.value = decodedText;
        try {
            targetInput.dispatchEvent(new Event('change', { bubbles: true }));
        } finally {
            delete targetInput.dataset.barcodeScanning;
        }
        targetInput.dispatchEvent(new CustomEvent('barcode-scanned', {
            bubbles: true,
            detail: scanDetail
        }));
    } else {
        document.dispatchEvent(new CustomEvent('barcode-scanned', {
            bubbles: true,
            detail: scanDetail
        }));
    }

    stopGlobalScanner();
}

window.startGlobalScanner = startGlobalScanner;
window.stopGlobalScanner = stopGlobalScanner;
window.onGlobalScanSuccess = onGlobalScanSuccess;
