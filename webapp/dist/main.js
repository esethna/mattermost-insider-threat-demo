// Hand-written plugin bundle: no build step. Uses the React instance the Mattermost webapp exposes.
(function () {
    const id = 'com.esethna.insider-threat-demo';
    const label = 'Start insider threat demo';

    function csrfToken() {
        const m = document.cookie.match(/(?:^|;\s*)MMCSRF=([^;]+)/);
        return m ? m[1] : '';
    }

    async function start(store) {
        const state = store.getState();
        const teamId = state.entities.teams.currentTeamId;
        const base = (window.basename || '').replace(/\/$/, '');
        let message;
        try {
            const resp = await fetch(`${base}/plugins/${id}/start`, {
                method: 'POST',
                credentials: 'same-origin',
                headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest', 'X-CSRF-Token': csrfToken()},
                body: JSON.stringify({team_id: teamId}),
            });
            message = resp.ok ? (await resp.json()).message : `The demo didn't start (${resp.status}).`;
        } catch (e) {
            message = "The demo didn't start: the server couldn't be reached.";
        }
        window.alert(message);
    }

    class DemoPlugin {
        initialize(registry, store) {
            const React = window.React;
            const icon = React.createElement('i', {className: 'icon icon-shield-alert-outline', style: {fontSize: '18px'}});
            registry.registerChannelHeaderButtonAction(icon, () => start(store), label, label);
        }
    }

    window.registerPlugin(id, new DemoPlugin());
})();
