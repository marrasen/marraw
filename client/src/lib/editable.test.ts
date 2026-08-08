// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';

import { isEditableTarget } from '@/lib/editable';

afterEach(() => {
  document.body.innerHTML = '';
});

function mount(html: string): HTMLElement {
  document.body.innerHTML = html;
  return document.body.firstElementChild as HTMLElement;
}

describe('isEditableTarget', () => {
  // Every one of these swallows a keystroke the user meant as text. The
  // shortcut dispatcher reads bare letters — p picks, x rejects, 1..5 rate —
  // so a miss here rates a photo while someone types a folder name.
  it('recognizes the fields the user types into', () => {
    for (const html of [
      '<input />',
      '<input type="number" />',
      '<textarea></textarea>',
      '<select><option>a</option></select>',
      '<div contenteditable="true"></div>',
    ]) {
      expect(isEditableTarget(mount(html))).toBe(true);
    }
  });

  // <select> was missing from the dispatcher's own copy of this check, so a
  // letter key aimed at an open dropdown reached the shortcuts.
  it('covers a select, which the dispatcher used to miss', () => {
    expect(isEditableTarget(mount('<select><option>a</option></select>'))).toBe(true);
  });

  // The other half of what the dispatcher missed: it looked only at the event
  // target, and an event can land on a node inside the field.
  it('looks through to an editable ancestor', () => {
    mount('<div contenteditable="true"><span id="inner">text</span></div>');
    expect(isEditableTarget(document.getElementById('inner'))).toBe(true);
  });

  it('lets ordinary chrome through', () => {
    for (const html of [
      '<div></div>',
      '<button>Pick</button>',
      '<img alt="photo" />',
      '<div contenteditable="false"></div>',
    ]) {
      expect(isEditableTarget(mount(html))).toBe(false);
    }
  });

  it('says no to what is not an element at all', () => {
    expect(isEditableTarget(null)).toBe(false);
    expect(isEditableTarget(document.createTextNode('x'))).toBe(false);
    expect(isEditableTarget(window)).toBe(false);
  });
});
