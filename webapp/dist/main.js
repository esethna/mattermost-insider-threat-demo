// Hand-written plugin bundle: no build step. Uses the React instance the Mattermost webapp exposes.
(function () {
    const id = 'com.esethna.insider-threat-demo';
    const label = 'Start insider threat demo';

    function csrfToken() {
        const m = document.cookie.match(/(?:^|;\s*)MMCSRF=([^;]+)/);
        return m ? m[1] : '';
    }

    // The server reports the outcome as an ephemeral post in the current channel; only failures
    // that never reach it fall back to an alert.
    async function start(store) {
        const state = store.getState();
        const teamId = state.entities.teams.currentTeamId;
        const channelId = state.entities.channels.currentChannelId;
        const base = (window.basename || '').replace(/\/$/, '');
        let failure;
        try {
            const resp = await fetch(`${base}/plugins/${id}/start`, {
                method: 'POST',
                credentials: 'same-origin',
                headers: {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest', 'X-CSRF-Token': csrfToken()},
                body: JSON.stringify({team_id: teamId, channel_id: channelId}),
            });
            if (!resp.ok) {
                failure = resp.status === 401 ? 'Your session has expired. Reload the page and sign in again.' : `The demo didn't start (${resp.status}).`;
            }
        } catch (e) {
            failure = "The demo didn't start: the server couldn't be reached.";
        }
        if (failure) {
            window.alert(failure);
        }
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
