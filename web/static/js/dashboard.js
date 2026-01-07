document.addEventListener('alpine:init', () => {
    Alpine.data('dashboardAlert', () => ({
        show: !localStorage.getItem('dismissedDashboardWallAlert'),
        dismiss() {
            this.show = false;
            localStorage.setItem('dismissedDashboardWallAlert', 'true');
        }
    }));
});
