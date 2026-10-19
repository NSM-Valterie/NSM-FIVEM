#pragma once

// NSM: redirect the client/server CNL endpoints (connect tickets, identity
// lookups) to the NSM masterlist service instead of Cfx's lambda service.
#define CNL_ENDPOINT "http://147.189.174.173/"
// CNL_HB_ENDPOINT defaults to CNL_ENDPOINT per CnlEndpoint.h
