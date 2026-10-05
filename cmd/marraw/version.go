package main

// renderVersion must match pyramid.RenderVersion, as the TypeScript client's
// RENDER_VERSION does: image URLs carry it, so a change to the rendering
// changes them. Kept here so a nobackend build needs no part of the backend;
// a test holds the two together.
const renderVersion = "r13"
