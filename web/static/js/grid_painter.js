document.addEventListener('alpine:init', () => {
    Alpine.data('gridPainter', (ctrlId, binsDataId, containersDataId, canEdit) => ({
        canEdit: canEdit,
        containers: [],
        selectedContainerIndex: 0,
        cells: {},
        confirmMessage: '',
        pendingAction: null,

        init() {
            // Helper to decode HTML entities if the proxy escapes JSON content
            const decodeHtml = (html) => {
                const txt = document.createElement("textarea");
                txt.innerHTML = html;
                return txt.value;
            };

            // Retrieve and Parse Data safely
            const rawContainers = document.getElementById(containersDataId).textContent;
            const rawBins = document.getElementById(binsDataId).textContent;

            const initialContainers = JSON.parse(decodeHtml(rawContainers));
            const existingBins = JSON.parse(decodeHtml(rawBins)) || [];

            if (initialContainers && initialContainers.length > 0) {
                this.containers = initialContainers.map(c => ({
                    id: c.id,
                    name: c.name,
                    segment_id: c.segment_id,
                    config: this.parseConfig(c.config_json?.String || "{}")
                }));
            } else {
                this.addContainer();
            }

            // Load Existing Bins
            existingBins.forEach(b => {
                const led = b.led_index?.Int64 || 0;
                const cID = b.container_id;

                // Find container index by ID
                const cIdx = this.containers.findIndex(c => c.id === cID);
                if (cIdx !== -1) {
                    const localIdx = this.getLocalIndexFromGridPos(cIdx, b.grid_x?.Int64 || 0, b.grid_y?.Int64 || 0);
                    if (localIdx !== -1) {
                        const key = `${cIdx},${localIdx}`;
                        this.cells[key] = {
                            led_index: Number(led),
                            name: b.name
                        };
                    }
                }
            });
        },

        parseConfig(jsonStr) {
            const defaultConfig = {
                type: 'grid',
                rows: 8,
                cols: 8,
                total: 64,
                start_corner: 'tl',
                sections: [{ rows: 4, cols: 4 }]
            };

            if (!jsonStr || typeof jsonStr !== 'string') {
                return defaultConfig;
            }

            try {
                const cfg = JSON.parse(jsonStr);

                if (!cfg || typeof cfg !== 'object') {
                    return defaultConfig;
                }

                return {
                    type: cfg.type || 'grid',
                    rows: Number(cfg.rows) || 8,
                    cols: Number(cfg.cols) || 8,
                    total: Number(cfg.total) || 64,
                    start_corner: cfg.start_corner || 'tl',
                    sections: Array.isArray(cfg.sections) ? cfg.sections : [{ rows: 4, cols: 4 }]
                };
            } catch (e) {
                console.warn('Error parsing container config:', e);
                return defaultConfig;
            }
        },

        addContainer() {
            this.containers.push({
                id: null,
                name: "New Container " + (this.containers.length + 1),
                segment_id: 0,
                config: { type: 'grid', rows: 8, cols: 8, start_corner: 'tl', sections: [{ rows: 4, cols: 4 }] }
            });
            this.selectedContainerIndex = this.containers.length - 1;
        },

        removeContainer(idx) {
            this.containers.splice(idx, 1);
            // Clear cells for this container and shift others
            const newCells = {};
            Object.keys(this.cells).forEach(key => {
                const [cIdx, lIdx] = key.split(',').map(Number);
                if (cIdx < idx) {
                    newCells[key] = this.cells[key];
                } else if (cIdx > idx) {
                    newCells[`${cIdx - 1},${lIdx}`] = this.cells[key];
                }
            });
            this.cells = newCells;
            if (this.selectedContainerIndex >= this.containers.length) {
                this.selectedContainerIndex = Math.max(0, this.containers.length - 1);
            }
        },

        get currentContainer() {
            return this.containers[this.selectedContainerIndex];
        },

        getRenderSections(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return [{ rows: 1, cols: cfg.total }];
            if (cfg.type === 'grid') return [{ rows: cfg.rows, cols: cfg.cols }];
            return cfg.sections;
        },

        getSectionBaseIndex(cIdx, secIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type !== 'compound') return 0;
            let count = 0;
            for (let i = 0; i < secIdx; i++) {
                const s = cfg.sections[i];
                count += (s.rows * s.cols);
            }
            return count;
        },

        getXY(cIdx, cellIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return { x: cellIdx, y: 0 };
            if (cfg.type === 'grid') return { x: cellIdx % cfg.cols, y: Math.floor(cellIdx / cfg.cols) };

            // Compound
            let count = 0;
            for (let sIdx = 0; sIdx < cfg.sections.length; sIdx++) {
                const s = cfg.sections[sIdx];
                const sSize = s.rows * s.cols;
                if (cellIdx < count + sSize) {
                    const localIdx = cellIdx - count;
                    return { x: localIdx % s.cols, y: Math.floor(localIdx / s.cols), sectionIndex: sIdx };
                }
                count += sSize;
            }
            return { x: 0, y: 0 };
        },

        getLocalIndexFromGridPos(cIdx, gx, gy) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return gx;
            if (cfg.type === 'grid') return (gy * cfg.cols) + gx;

            // Compound
            let currentYBase = 0;
            let currentCountBase = 0;
            for (let i = 0; i < cfg.sections.length; i++) {
                const s = cfg.sections[i];
                if (gy >= currentYBase && gy < currentYBase + s.rows) {
                    const localY = gy - currentYBase;
                    if (gx < s.cols) {
                        return currentCountBase + (localY * s.cols) + gx;
                    }
                }
                currentYBase += (s.rows + 1);
                currentCountBase += (s.rows * s.cols);
            }
            return -1;
        },

        getCellClass(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            return this.cells[key] ? 'bg-primary text-primary-content border-primary' : 'bg-base-100 text-base-content/20';
        },

        getGlobalLedIndex(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            if (!this.cells[key]) return '';
            return this.cells[key].led_index;
        },

        getContainerTotalLeds(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return cfg.total;
            if (cfg.type === 'grid') return cfg.rows * cfg.cols;
            return cfg.sections.reduce((sum, s) => sum + (s.rows * s.cols), 0);
        },

        getBinName(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            return this.cells[key] ? this.cells[key].name : '';
        },

        toggleCell(cIdx, cellIdx) {
            if (!this.canEdit) return;
            const key = `${cIdx},${cellIdx}`;
            if (this.cells[key]) {
                delete this.cells[key];
            } else {
                const nextIndex = this.getNextAvailableLedIndex(cIdx);
                const name = this.generateName(cIdx, cellIdx);
                this.cells[key] = { led_index: nextIndex, name: name };
            }
        },

        getNextAvailableLedIndex(cIdx) {
            const targetSegment = this.containers[cIdx].segment_id;
            const used = new Set();

            Object.keys(this.cells).forEach(key => {
                const [ci, _] = key.split(',').map(Number);
                if (this.containers[ci].segment_id === targetSegment) {
                    used.add(this.cells[key].led_index);
                }
            });

            let i = 0;
            while (used.has(i)) i++;
            return i;
        },

        generateName(cIdx, cellIdx) {
            const { x, y, sectionIndex } = this.getXY(cIdx, cellIdx);
            const charCode = 65 + (x % 26);
            const char = String.fromCharCode(charCode);
            const colLetter = char.repeat(Math.floor(x / 26) + 1);
            const baseName = `${colLetter}${y + 1}`;

            if (this.containers[cIdx].config.type === 'compound' && sectionIndex !== undefined) {
                return `S${sectionIndex + 1}-${baseName}`;
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

            // Clear cells for current container
            Object.keys(this.cells).forEach(key => {
                if (key.startsWith(cIdx + ",")) delete this.cells[key];
            });

            let ledCounter = this.getSegmentStartOffset(cIdx);
            const startPos = cfg.start_corner || 'tl';
            const sections = this.getRenderSections(cIdx);
            let currentYOffset = 0;

            sections.forEach((sec, secIdx) => {
                for (let r = 0; r < sec.rows; r++) {
                    for (let c = 0; c < sec.cols; c++) {
                        let visualY = (startPos.includes('b')) ? (sec.rows - 1 - r) : r;
                        let visualX = (startPos.includes('r')) ? (sec.cols - 1 - c) : c;

                        if (mode === 'serpentine' && r % 2 !== 0) {
                            visualX = startPos.includes('r') ? c : (sec.cols - 1 - c);
                        }

                        const globalY = currentYOffset + visualY;
                        const localIdx = this.getLocalIndexFromGridPos(cIdx, visualX, globalY);
                        if (localIdx !== -1) {
                            this.cells[`${cIdx},${localIdx}`] = {
                                led_index: ledCounter,
                                name: this.generateName(cIdx, localIdx)
                            };
                        }
                        ledCounter++;
                    }
                }
                currentYOffset += (sec.rows + 1);
            });
        },

        askConfirm(action) {
            this.pendingAction = action;
            if (action === 'clear') {
                this.confirmMessage = "Are you sure you want to clear all mappings for this container? This cannot be undone.";
            } else if (action === 'save') {
                this.confirmMessage = "Are you sure you want to save these changes? This will overwrite the existing configuration.";
            }
            document.getElementById('grid_painter_confirm_modal').showModal();
        },

        confirmAction() {
            if (this.pendingAction === 'clear') {
                this.clearGrid();
            } else if (this.pendingAction === 'save') {
                this.submitGrid();
            }
            document.getElementById('grid_painter_confirm_modal').close();
            this.pendingAction = null;
        },

        clearGrid() {
            const cIdx = this.selectedContainerIndex;
            Object.keys(this.cells).forEach(key => {
                if (key.startsWith(cIdx + ",")) delete this.cells[key];
            });
        },

        submitGrid() {
            this.$refs.gridForm.submit();
        },

        exportBinData() {
            const result = [];
            this.containers.forEach((c, cIdx) => {
                const sections = this.getRenderSections(cIdx);
                let globalYOffset = 0;
                let countBase = 0;
                sections.forEach((sec, secIdx) => {
                    for (let i = 0; i < (sec.rows * sec.cols); i++) {
                        const localIdx = countBase + i;
                        const key = `${cIdx},${localIdx}`;
                        const cell = this.cells[key];
                        if (cell) {
                            const { x, y } = this.getXY(cIdx, localIdx);
                            result.push({
                                container_index: cIdx,
                                x: x,
                                y: y + globalYOffset,
                                led_index: cell.led_index,
                                name: cell.name
                            });
                        }
                    }
                    globalYOffset += (sec.rows + 1);
                    countBase += (sec.rows * sec.cols);
                });
            });
            return result;
        },

        exportContainerData() {
            return this.containers.map(c => ({
                id: c.id,
                name: c.name,
                segment_id: c.segment_id,
                config: c.config
            }));
        }
    }));
});
