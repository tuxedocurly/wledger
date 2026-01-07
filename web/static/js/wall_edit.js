document.addEventListener('alpine:init', () => {
    Alpine.data('wallEdit', (wallId, wallDataId, allDataId) => ({
        wallContainers: [],
        availableContainers: [],
        selectedAddId: '',

        init() {
            const decodeHtml = (html) => {
                const txt = document.createElement("textarea");
                txt.innerHTML = html;
                return txt.value;
            };

            const rawWall = document.getElementById(wallDataId).textContent;
            const rawAll = document.getElementById(allDataId).textContent;

            this.wallContainers = JSON.parse(decodeHtml(rawWall));
            this.availableContainers = JSON.parse(decodeHtml(rawAll));

            this.$nextTick(() => {
                new Sortable(this.$refs.wallContainerList, {
                    handle: '.drag-handle',
                    draggable: '.group',
                    animation: 150,
                    ghostClass: 'sortable-ghost',
                    chosenClass: 'sortable-chosen',
                    dragClass: 'sortable-drag',
                    onStart: () => {
                        if (window.navigator.vibrate) window.navigator.vibrate(5);
                    },
                    onEnd: (evt) => {
                        const newOrder = [...this.wallContainers];
                        const item = newOrder.splice(evt.oldIndex, 1)[0];
                        newOrder.splice(evt.newIndex, 0, item);
                        
                        this.wallContainers = [];
                        this.$nextTick(() => {
                            this.wallContainers = newOrder;
                            if (window.navigator.vibrate) window.navigator.vibrate(10);
                        });
                    }
                });
            });
        },

        addToWall() {
            if (this.selectedAddId) {
                const c = this.availableContainers.find(x => x.id == this.selectedAddId);
                if (c && !this.wallContainers.some(x => x.id == c.id)) {
                    this.wallContainers.push(c);
                    this.selectedAddId = '';
                }
            }
        },

        deleteWall() {
            if (confirm('Delete this wall?')) {
                const f = document.createElement('form');
                f.method = 'POST';
                f.action = '/walls/' + wallId + '/delete';
                document.body.appendChild(f);
                f.submit();
            }
        }
    }));
});
