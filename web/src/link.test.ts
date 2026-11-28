import { expect, test } from 'vitest';
import { launcherLink } from './link';

const key = '0123456789abcdef';

test("a room's button names the server and the player, and no game: the launcher goes by their room", () => {
  expect(launcherLink({ host: 'zombies.example.com', tls: true, key, solo: false }))
    .toBe('zombies-launcher://zombies.example.com/play?key=' + key);
  expect(launcherLink({ host: '10.0.50.11:3075', tls: false, key, solo: false, of: '', map: '' }))
    .toBe('zombies-launcher://10.0.50.11:3075/play?key=' + key + '&tls=0');
});

test('Play local names the game to get the PC ready for, and the map to download', () => {
  // Connect, at Black Ops II, which is named by no word, and at Black Ops III
  expect(launcherLink({ host: 'h', tls: true, key, solo: false, of: 't6' })).toBe('zombies-launcher://h/play?key=' + key);
  expect(launcherLink({ host: 'h', tls: true, key, solo: false, of: 't7' }))
    .toBe('zombies-launcher://h/play?key=' + key + '&of=t7');
  // a map that the PC lacks
  expect(launcherLink({ host: 'h:3075', tls: false, key, solo: false, of: 't7', map: 'zm_tomb' }))
    .toBe('zombies-launcher://h:3075/play?key=' + key + '&of=t7&map=zm_tomb&tls=0');
});

test('Launch game asks to play alone, under the name, with the key only where the page has one', () => {
  expect(launcherLink({ host: 'h', tls: true, key, solo: true, name: 'Ann', of: 't7' }))
    .toBe('zombies-launcher://h/play?solo=1&name=Ann&of=t7&key=' + key);
  // a room's Launch game names the room's map
  expect(launcherLink({ host: 'h', tls: true, key, solo: true, name: 'Ann', of: 't7', map: 'zm_factory' }))
    .toBe('zombies-launcher://h/play?solo=1&name=Ann&of=t7&map=zm_factory&key=' + key);
  // a page that can't reach its server has no key to send
  expect(launcherLink({ host: 'h', tls: false, key: '', solo: true, name: 'Ann', of: 't6' }))
    .toBe('zombies-launcher://h/play?solo=1&name=Ann&tls=0');
  // a name is down to letters, digits and %
  expect(launcherLink({ host: 'h', tls: true, key, solo: true, name: "Ann B's (1)!" }))
    .toBe('zombies-launcher://h/play?solo=1&name=Ann%20B%27s%20%281%29%21&key=' + key);
});
