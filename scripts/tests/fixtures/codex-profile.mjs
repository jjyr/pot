import {mountApp} from '../../../web/src/components/ui.js';
import {createProductionClient} from '../../../web/src/adapters/production.mjs';
const client=await createProductionClient();
window.codexProfileFixture={client,dispose:mountApp(document.querySelector('#app'),client)};
