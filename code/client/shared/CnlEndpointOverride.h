#pragma once

// NSM: redirect the client/server CNL endpoints (connect tickets, identity
// lookups) to the NSM masterlist service instead of Cfx's lambda service.
#define CNL_ENDPOINT "http://194.104.146.133/"
// CNL_HB_ENDPOINT defaults to CNL_ENDPOINT per CnlEndpoint.h
