document.addEventListener('alpine:init', () => {
    Alpine.data('gridPainter', (ctrlId, binsDataId, containersDataId, canEdit, isStandalone = false) => ({
        canEdit: canEdit,
        isStandalone: isStandalone,
        containers: [],
        selectedContainerIndex: 0,
        selectedCellKey: null,
        editMode: 'paint', // 'paint' | 'erase'
        defaultBinWidth: 1,
        cells: {},
        confirmMessage: '',
        pendingAction: null,

        init() {
            const decodeHtml = (html) => {
                const txt = document.createElement('textarea');
                txt.innerHTML = html;
                return txt.value;
            };

            const rawContainers = document.getElementById(containersDataId).textContent;
            const rawBins = document.getElementById(binsDataId).textContent;

            const initialContainers = JSON.parse(decodeHtml(rawContainers));
            const existingBins = JSON.parse(decodeHtml(rawBins)) || [];

            if (initialContainers && initialContainers.length > 0) {
                this.containers = initialContainers.map(c => ({
                    id: c.id,
                    name: c.name,
                    segment_id: Number(c.segment_id) || 0,
                    config: this.parseConfig(c.config_json?.String || '{}')
                }));
            } else {
                this.addContainer();
            }

            existingBins.forEach(b => {
                const cID = b.container_id;
                const cIdx = this.containers.findIndex(c => c.id === cID);
                if (cIdx === -1) return;

                const gx = Number(b.grid_x?.Int64 || 0);
                const gy = Number(b.grid_y?.Int64 || 0);
                const pos = this.resolveSectionCoords(cIdx, gx, gy);
                if (!pos) return;

                const led = (b.led_index && b.led_index.Valid) ? Number(b.led_index.Int64) : null;
                const width = Math.max(1, Number(b.width?.Int64 || 1));
                const key = this.cellKey(cIdx, pos.secIdx, pos.x, pos.y);
                this.cells[key] = {
                    led_index: led,
                    width: width,
                    name: b.name || this.generateName(cIdx, pos.secIdx, pos.x, pos.y)
                };
            });
        },

        parseConfig(jsonStr) {
            const defaultConfig = {
                type: 'grid',
                rows: 8,
                cols: 8,
                total: 64,
                start_corner: 'tl',
                sections: [{ rows: 4, cols: 4 }],
                x_start_led: 0,
                x_reverse: false,
                y_start_led: 8,
                y_reverse: false,
                y_auto: true
            };

            if (!jsonStr || typeof jsonStr !== 'string') {
                return defaultConfig;
            }

            try {
                const cfg = JSON.parse(jsonStr);
                if (!cfg || typeof cfg !== 'object') {
                    return defaultConfig;
                }
                const rows = Math.max(1, Number(cfg.rows) || 8);
                const cols = Math.max(1, Number(cfg.cols) || 8);
                const xStart = (cfg.x_start_led !== undefined && cfg.x_start_led !== null && !Number.isNaN(Number(cfg.x_start_led)))
                    ? Math.max(0, Number(cfg.x_start_led))
                    : 0;
                const defaultYStart = xStart + cols;
                const yStart = (cfg.y_start_led !== undefined && cfg.y_start_led !== null && !Number.isNaN(Number(cfg.y_start_led)))
                    ? Math.max(0, Number(cfg.y_start_led))
                    : defaultYStart;

                return {
                    type: cfg.type || 'grid',
                    rows: rows,
                    cols: cols,
                    total: Math.max(1, Number(cfg.total) || 64),
                    start_corner: cfg.start_corner || 'tl',
                    sections: Array.isArray(cfg.sections) && cfg.sections.length > 0
                        ? cfg.sections.map(s => ({ rows: Math.max(1, Number(s.rows) || 4), cols: Math.max(1, Number(s.cols) || 4) }))
                        : [{ rows: 4, cols: 4 }],
                    x_start_led: xStart,
                    x_reverse: Boolean(cfg.x_reverse),
                    y_start_led: yStart,
                    y_reverse: Boolean(cfg.y_reverse),
                    y_auto: yStart === defaultYStart
                };
            } catch (e) {
                console.warn('Error parsing container config:', e);
                return defaultConfig;
            }
        },

        cellKey(cIdx, secIdx, x, y) {
            return `${cIdx},${secIdx},${x},${y}`;
        },

        parseCellKey(key) {
            const parts = key.split(',').map(Number);
            if (parts.length !== 4) return null;
            return { cIdx: parts[0], secIdx: parts[1], x: parts[2], y: parts[3] };
        },

        resolveSectionCoords(cIdx, gx, gy) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type !== 'compound') {
                return { secIdx: 0, x: gx, y: gy };
            }
            let currentYBase = 0;
            for (let sIdx = 0; sIdx < cfg.sections.length; sIdx++) {
                const s = cfg.sections[sIdx];
                if (gy >= currentYBase && gy < currentYBase + s.rows) {
                    return { secIdx: sIdx, x: gx, y: gy - currentYBase };
                }
                currentYBase += (s.rows + 1);
            }
            return { secIdx: 0, x: gx, y: gy };
        },

        addContainer() {
            if (this.isStandalone && this.containers.length >= 1) return;
            this.containers.push({
                id: null,
                name: 'New Container ' + (this.containers.length + 1),
                segment_id: 0,
                config: {
                    type: 'grid',
                    rows: 8,
                    cols: 8,
                    total: 64,
                    start_corner: 'tl',
                    sections: [{ rows: 4, cols: 4 }],
                    x_start_led: 0,
                    x_reverse: false,
                    y_start_led: 8,
                    y_reverse: false,
                    y_auto: true
                }
            });
            this.selectedContainerIndex = this.containers.length - 1;
            this.selectedCellKey = null;
        },

        onLayoutTypeChange() {
            const c = this.currentContainer;
            if (!c || !c.config || c.config.type !== 'xy') return;

            c.config.rows = Math.max(1, Number(c.config.rows) || 1);
            c.config.cols = Math.max(1, Number(c.config.cols) || 1);
            if (this.selectedContainerIndex > 0 && Number(c.config.x_start_led) === 0 && c.config.y_auto) {
                const offset = this.getSegmentStartOffset(this.selectedContainerIndex);
                c.config.x_start_led = offset;
                c.config.y_start_led = offset + c.config.cols;
            } else {
                this.onXYDimensionsChange();
            }

            Object.keys(this.cells).forEach(key => {
                const parsed = this.parseCellKey(key);
                if (!parsed || parsed.cIdx !== this.selectedContainerIndex) return;
                this.cells[key] = {
                    led_index: null,
                    width: 1,
                    name: this.cells[key].name
                };
            });
        },

        onXYDimensionsChange() {
            const c = this.currentContainer;
            if (!c || !c.config || c.config.type !== 'xy' || !c.config.y_auto) return;
            const xStart = Math.max(0, Number(c.config.x_start_led) || 0);
            const cols = Math.max(1, Number(c.config.cols) || 1);
            c.config.y_start_led = xStart + cols;
        },

        removeContainer(idx) {
            if (this.isStandalone) return;
            this.containers.splice(idx, 1);
            const newCells = {};
            Object.keys(this.cells).forEach(key => {
                const parsed = this.parseCellKey(key);
                if (!parsed) return;
                if (parsed.cIdx < idx) {
                    newCells[key] = this.cells[key];
                } else if (parsed.cIdx > idx) {
                    newCells[this.cellKey(parsed.cIdx - 1, parsed.secIdx, parsed.x, parsed.y)] = this.cells[key];
                }
            });
            this.cells = newCells;
            this.selectedCellKey = null;
            if (this.selectedContainerIndex >= this.containers.length) {
                this.selectedContainerIndex = Math.max(0, this.containers.length - 1);
            }
        },

        get currentContainer() {
            return this.containers[this.selectedContainerIndex];
        },

        get selectedCell() {
            if (!this.selectedCellKey || !this.cells[this.selectedCellKey]) return null;
            return this.cells[this.selectedCellKey];
        },

        getRenderSections(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return [{ rows: 1, cols: Math.max(1, Number(cfg.total) || 1) }];
            if (cfg.type === 'grid' || cfg.type === 'xy') return [{ rows: Math.max(1, Number(cfg.rows) || 1), cols: Math.max(1, Number(cfg.cols) || 1) }];
            return (cfg.sections || []).map(s => ({
                rows: Math.max(1, Number(s.rows) || 1),
                cols: Math.max(1, Number(s.cols) || 1)
            }));
        },

        getXYPair(cIdx, x, y) {
            const cfg = this.containers[cIdx].config;
            const cols = Math.max(1, Number(cfg.cols) || 1);
            const rows = Math.max(1, Number(cfg.rows) || 1);
            const xStart = Math.max(0, Number(cfg.x_start_led) || 0);
            const yStart = (cfg.y_start_led !== undefined && cfg.y_start_led !== null && !Number.isNaN(Number(cfg.y_start_led)))
                ? Math.max(0, Number(cfg.y_start_led))
                : (xStart + cols);
            const xLed = xStart + (cfg.x_reverse ? (cols - 1 - x) : x);
            const yLed = yStart + (cfg.y_reverse ? (rows - 1 - y) : y);
            return { xLed, yLed };
        },

        cellXY(secCols, oneBasedIdx) {
            const zeroIdx = oneBasedIdx - 1;
            return {
                x: zeroIdx % secCols,
                y: Math.floor(zeroIdx / secCols)
            };
        },

        getCellClass(cIdx, secIdx, x, y) {
            const key = this.cellKey(cIdx, secIdx, x, y);
            if (!this.cells[key]) {
                return 'bg-base-100 text-base-content/20';
            }
            if (this.selectedCellKey === key) {
                return 'bg-primary text-primary-content border-accent ring-2 ring-accent';
            }
            return 'bg-primary text-primary-content border-primary';
        },

        getGlobalLedIndex(cIdx, secIdx, x, y) {
            const key = this.cellKey(cIdx, secIdx, x, y);
            const cell = this.cells[key];
            if (!cell) return '';
            if (this.containers[cIdx].config.type === 'xy' && !this.isStandalone) {
                const pair = this.getXYPair(cIdx, x, y);
                return `X${pair.xLed}·Y${pair.yLed}`;
            }
            if (cell.led_index === null || cell.led_index === undefined) return 'BIN';
            return cell.led_index;
        },

        getCellWidth(cIdx, secIdx, x, y) {
            const key = this.cellKey(cIdx, secIdx, x, y);
            const cell = this.cells[key];
            if (!cell) return 1;
            return Math.max(1, Number(cell.width) || 1);
        },

        getContainerTotalLeds(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'xy') {
                return Math.max(1, Number(cfg.cols) || 1) + Math.max(1, Number(cfg.rows) || 1);
            }
            const sections = this.getRenderSections(cIdx);
            let mappedMax = 0;
            let totalWidth = 0;
            sections.forEach((sec, secIdx) => {
                for (let y = 0; y < sec.rows; y++) {
                    for (let x = 0; x < sec.cols; x++) {
                        const cell = this.cells[this.cellKey(cIdx, secIdx, x, y)];
                        if (cell && cell.led_index !== null && cell.led_index !== undefined) {
                            const w = Math.max(1, Number(cell.width) || 1);
                            totalWidth += w;
                            mappedMax = Math.max(mappedMax, Number(cell.led_index) + w);
                        }
                    }
                }
            });
            if (mappedMax > 0) return Math.max(mappedMax, totalWidth);
            return sections.reduce((sum, s) => sum + (s.rows * s.cols), 0);
        },

        getBinName(cIdx, secIdx, x, y) {
            const key = this.cellKey(cIdx, secIdx, x, y);
            return this.cells[key] ? this.cells[key].name : '';
        },

        clickCell(cIdx, secIdx, x, y) {
            if (!this.canEdit) return;
            this.selectedContainerIndex = cIdx;
            const key = this.cellKey(cIdx, secIdx, x, y);

            if (this.editMode === 'erase') {
                if (this.cells[key]) {
                    delete this.cells[key];
                    if (this.selectedCellKey === key) this.selectedCellKey = null;
                }
                return;
            }

            if (this.cells[key]) {
                this.selectedCellKey = key;
                return;
            }

            const isXY = this.containers[cIdx].config.type === 'xy';
            const width = isXY ? 1 : Math.max(1, Number(this.defaultBinWidth) || 1);
            const nextIndex = (this.isStandalone || isXY) ? null : this.getNextAvailableLedIndex(cIdx, width);
            const name = this.generateName(cIdx, secIdx, x, y);
            this.cells[key] = {
                led_index: nextIndex,
                width: width,
                name: name
            };
            this.selectedCellKey = key;
        },

        removeSelectedCell() {
            if (!this.canEdit || !this.selectedCellKey) return;
            delete this.cells[this.selectedCellKey];
            this.selectedCellKey = null;
        },

        toggleSelectedLedMapping(enabled) {
            if (!this.selectedCell) return;
            if (enabled) {
                const width = Math.max(1, Number(this.selectedCell.width) || 1);
                this.selectedCell.led_index = this.getNextAvailableLedIndex(this.selectedContainerIndex, width);
            } else {
                this.selectedCell.led_index = null;
            }
        },

        getNextAvailableLedIndex(cIdx, neededWidth = 1) {
            const targetSegment = this.containers[cIdx].segment_id;
            const used = new Set();

            Object.keys(this.cells).forEach(key => {
                const parsed = this.parseCellKey(key);
                if (!parsed) return;
                const container = this.containers[parsed.cIdx];
                if (!container || container.segment_id !== targetSegment) return;
                const cell = this.cells[key];
                if (cell.led_index === null || cell.led_index === undefined) return;
                const start = Number(cell.led_index);
                const w = Math.max(1, Number(cell.width) || 1);
                for (let offset = 0; offset < w; offset++) {
                    used.add(start + offset);
                }
            });

            this.containers.forEach(container => {
                if (!container || container.segment_id !== targetSegment || !container.config || container.config.type !== 'xy') {
                    return;
                }
                const cols = Math.max(1, Number(container.config.cols) || 1);
                const rows = Math.max(1, Number(container.config.rows) || 1);
                const xStart = Math.max(0, Number(container.config.x_start_led) || 0);
                const yStart = (container.config.y_start_led !== undefined && container.config.y_start_led !== null && !Number.isNaN(Number(container.config.y_start_led)))
                    ? Math.max(0, Number(container.config.y_start_led))
                    : (xStart + cols);
                for (let offset = 0; offset < cols; offset++) {
                    used.add(xStart + offset);
                }
                for (let offset = 0; offset < rows; offset++) {
                    used.add(yStart + offset);
                }
            });

            const span = Math.max(1, Number(neededWidth) || 1);
            let candidate = 0;
            while (true) {
                let fits = true;
                for (let offset = 0; offset < span; offset++) {
                    if (used.has(candidate + offset)) {
                        fits = false;
                        break;
                    }
                }
                if (fits) return candidate;
                candidate++;
            }
        },

        columnName(x) {
            let n = x;
            let res = '';
            while (n >= 0) {
                res = String.fromCharCode(65 + (n % 26)) + res;
                n = Math.floor(n / 26) - 1;
            }
            return res || 'A';
        },

        generateName(cIdx, secIdx, x, y) {
            const colLetter = this.columnName(x);
            const baseName = `${colLetter}${y + 1}`;

            if (this.containers[cIdx].config.type === 'compound') {
                return `S${secIdx + 1}-${baseName}`;
            }
            if (this.containers.length > 1) {
                return `C${cIdx + 1}-${baseName}`;
            }
            return baseName;
        },

        getSegmentStartOffset(cIdx) {
            const targetSegment = this.containers[cIdx].segment_id;
            let offset = 0;
            for (let i = 0; i < cIdx; i++) {
                if (this.containers[i].segment_id === targetSegment) {
                    offset += this.getContainerTotalLeds(i);
                }
            }
            return offset;
        },

        autoFill(mode) {
            if (!this.canEdit) return;
            const cIdx = this.selectedContainerIndex;
            const cfg = this.containers[cIdx].config;

            this.clearContainerCells(cIdx);

            if (mode === 'xy' || cfg.type === 'xy') {
                const sections = this.getRenderSections(cIdx);
                sections.forEach((sec, secIdx) => {
                    for (let y = 0; y < sec.rows; y++) {
                        for (let x = 0; x < sec.cols; x++) {
                            const key = this.cellKey(cIdx, secIdx, x, y);
                            this.cells[key] = {
                                led_index: null,
                                width: 1,
                                name: this.generateName(cIdx, secIdx, x, y)
                            };
                        }
                    }
                });
                this.selectedCellKey = null;
                return;
            }

            const binWidth = Math.max(1, Number(this.defaultBinWidth) || 1);
            let ledCounter = this.getSegmentStartOffset(cIdx);
            const startPos = cfg.start_corner || 'tl';
            const sections = this.getRenderSections(cIdx);

            sections.forEach((sec, secIdx) => {
                for (let r = 0; r < sec.rows; r++) {
                    for (let c = 0; c < sec.cols; c++) {
                        const visualY = startPos.includes('b') ? (sec.rows - 1 - r) : r;
                        let visualX = startPos.includes('r') ? (sec.cols - 1 - c) : c;

                        if (mode === 'serpentine' && r % 2 !== 0) {
                            visualX = startPos.includes('r') ? c : (sec.cols - 1 - c);
                        }

                        const key = this.cellKey(cIdx, secIdx, visualX, visualY);
                        this.cells[key] = {
                            led_index: mode === 'standalone' ? null : ledCounter,
                            width: binWidth,
                            name: this.generateName(cIdx, secIdx, visualX, visualY)
                        };
                        ledCounter += binWidth;
                    }
                }
            });
            this.selectedCellKey = null;
        },

        clearContainerCells(cIdx) {
            Object.keys(this.cells).forEach(key => {
                const parsed = this.parseCellKey(key);
                if (parsed && parsed.cIdx === cIdx) {
                    delete this.cells[key];
                }
            });
            if (this.selectedCellKey && this.selectedCellKey.startsWith(cIdx + ',')) {
                this.selectedCellKey = null;
            }
        },

        askConfirm(action) {
            this.pendingAction = action;
            if (action === 'clear') {
                this.confirmMessage = 'Are you sure you want to clear all mappings for this container? This cannot be undone.';
            } else if (action === 'save') {
                this.confirmMessage = 'Are you sure you want to save these changes? This will overwrite the existing configuration.';
            }
            document.getElementById('grid_painter_confirm_modal').showModal();
        },

        confirmAction() {
            if (this.pendingAction === 'clear') {
                this.clearContainerCells(this.selectedContainerIndex);
            } else if (this.pendingAction === 'save') {
                this.submitGrid();
            }
            document.getElementById('grid_painter_confirm_modal').close();
            this.pendingAction = null;
        },

        submitGrid() {
            this.$refs.gridForm.submit();
        },

        exportBinData() {
            const result = [];
            this.containers.forEach((c, cIdx) => {
                const sections = this.getRenderSections(cIdx);
                let globalYOffset = 0;

                sections.forEach((sec, secIdx) => {
                    for (let y = 0; y < sec.rows; y++) {
                        for (let x = 0; x < sec.cols; x++) {
                            const key = this.cellKey(cIdx, secIdx, x, y);
                            const cell = this.cells[key];
                            if (!cell) continue;

                            const isXY = c.config.type === 'xy';
                            const rawLed = cell.led_index;
                            const ledIndex = (isXY || rawLed === null || rawLed === undefined || rawLed === '')
                                ? null
                                : Math.max(0, Number(rawLed));

                            result.push({
                                container_index: cIdx,
                                x: x,
                                y: y + globalYOffset,
                                led_index: ledIndex,
                                width: isXY ? 1 : Math.max(1, Number(cell.width) || 1),
                                name: cell.name
                            });
                        }
                    }
                    globalYOffset += (sec.rows + 1);
                });
            });
            return result;
        },

        exportContainerData() {
            return this.containers.map(c => {
                const cols = Math.max(1, Number(c.config.cols) || 1);
                const rows = Math.max(1, Number(c.config.rows) || 1);
                const config = {
                    type: c.config.type || 'grid',
                    rows: rows,
                    cols: cols,
                    total: Math.max(1, Number(c.config.total) || 1),
                    start_corner: c.config.start_corner || 'tl',
                    sections: (c.config.sections || []).map(s => ({
                        rows: Math.max(1, Number(s.rows) || 1),
                        cols: Math.max(1, Number(s.cols) || 1)
                    }))
                };
                if (config.type === 'xy') {
                    const xStart = Math.max(0, Number(c.config.x_start_led) || 0);
                    const yStart = (c.config.y_start_led !== undefined && c.config.y_start_led !== null && !Number.isNaN(Number(c.config.y_start_led)))
                        ? Math.max(0, Number(c.config.y_start_led))
                        : (xStart + cols);
                    config.x_start_led = xStart;
                    config.x_reverse = Boolean(c.config.x_reverse);
                    config.y_start_led = yStart;
                    config.y_reverse = Boolean(c.config.y_reverse);
                }
                return {
                    id: c.id,
                    name: c.name,
                    segment_id: Number(c.segment_id) || 0,
                    config: config
                };
            });
        }
    }));
});
