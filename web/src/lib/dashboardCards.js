import AboveTimeline from '../components/AboveTimeline.svelte';
import CurrentSightings from '../components/CurrentSightings.svelte';

import MetricFlightsSeen from '../components/MetricFlightsSeen.svelte';
import MetricAircraftSeen from '../components/MetricAircraftSeen.svelte';
import ActivityFlightsByPeriod from '../components/ActivityFlightsByPeriod.svelte';
import ActivityAircraftByPeriod from '../components/ActivityAircraftByPeriod.svelte';
import ActivityTopTypesByPeriod from '../components/ActivityTopTypesByPeriod.svelte';

import RouteTopAirlines from '../components/RouteTopAirlines.svelte';
import RouteTopAirportsDomestic from '../components/RouteTopAirportsDomestic.svelte';
import RouteTopAirportsInternational from '../components/RouteTopAirportsInternational.svelte';
import RouteTopRoutes from '../components/RouteTopRoutes.svelte';
import RouteTopCountriesOrigin from '../components/RouteTopCountriesOrigin.svelte';
import RouteTopCountriesDestination from '../components/RouteTopCountriesDestination.svelte';

import InterestingMilAircraft from '../components/InterestingMilAircraft.svelte';
import InterestingGovAircraft from '../components/InterestingGovAircraft.svelte';
import InterestingPolAircraft from '../components/InterestingPolAircraft.svelte';
import InterestingCivAircraft from '../components/InterestingCivAircraft.svelte';

import MotionFastestAircraft from '../components/MotionFastestAircraft.svelte';
import MotionSlowestAircraft from '../components/MotionSlowestAircraft.svelte';
import MotionHighestAircraft from '../components/MotionHighestAircraft.svelte';
import MotionLowestAircraft from '../components/MotionLowestAircraft.svelte';
import MotionFurthestFlownAircraft from '../components/MotionFurthestFlownAircraft.svelte';
import MotionMostRemainingAircraft from '../components/MotionMostRemainingAircraft.svelte';
import MotionLongestRouteAircraft from '../components/MotionLongestRouteAircraft.svelte';

export const dashboardCards = [
    { id: 'above_timeline', title: 'Above Me', tab: 'global', component: AboveTimeline },
    { id: 'current_sightings', title: 'Current Sightings', tab: 'current-stat', component: CurrentSightings },

    { id: 'metric_flights_seen', title: 'Flights Seen', tab: 'activity', component: MetricFlightsSeen },
    { id: 'metric_aircraft_seen', title: 'Aircraft Seen', tab: 'activity', component: MetricAircraftSeen },
    { id: 'activity_flights_by_period', title: 'Flights by Period', tab: 'activity', component: ActivityFlightsByPeriod },
    { id: 'activity_aircraft_by_period', title: 'Aircraft by Period', tab: 'activity', component: ActivityAircraftByPeriod },
    { id: 'activity_top_types_flights', title: 'Top Types (Flights)', tab: 'activity', component: ActivityTopTypesByPeriod, props: { aircraftorflight: 'flights' } },
    { id: 'activity_top_types_aircraft', title: 'Top Types (Aircraft)', tab: 'activity', component: ActivityTopTypesByPeriod, props: { aircraftorflight: 'aircraft' } },

    { id: 'route_top_airlines', title: 'Top Airlines', tab: 'route-stat', component: RouteTopAirlines },
    { id: 'route_top_airports_domestic', title: 'Top Domestic Airports', tab: 'route-stat', component: RouteTopAirportsDomestic },
    { id: 'route_top_airports_international', title: 'Top International Airports', tab: 'route-stat', component: RouteTopAirportsInternational },
    { id: 'route_top_routes', title: 'Top Routes', tab: 'route-stat', component: RouteTopRoutes },
    { id: 'route_top_countries_origin', title: 'Top Origin Countries', tab: 'route-stat', component: RouteTopCountriesOrigin },
    { id: 'route_top_countries_destination', title: 'Top Destination Countries', tab: 'route-stat', component: RouteTopCountriesDestination },

    { id: 'interesting_military', title: 'Military Aircraft', tab: 'interesting-stat', component: InterestingMilAircraft },
    { id: 'interesting_government', title: 'Government Aircraft', tab: 'interesting-stat', component: InterestingGovAircraft },
    { id: 'interesting_police', title: 'Police Aircraft', tab: 'interesting-stat', component: InterestingPolAircraft },
    { id: 'interesting_civilian', title: 'Civilian Aircraft', tab: 'interesting-stat', component: InterestingCivAircraft },

    { id: 'motion_fastest', title: 'Fastest Aircraft', tab: 'motion-stat', component: MotionFastestAircraft },
    { id: 'motion_slowest', title: 'Slowest Aircraft', tab: 'motion-stat', component: MotionSlowestAircraft },
    { id: 'motion_highest', title: 'Highest Aircraft', tab: 'motion-stat', component: MotionHighestAircraft },
    { id: 'motion_lowest', title: 'Lowest Aircraft', tab: 'motion-stat', component: MotionLowestAircraft },
    { id: 'motion_furthest_flown', title: 'Furthest Flown', tab: 'motion-stat', component: MotionFurthestFlownAircraft },
    { id: 'motion_most_remaining', title: 'Most Remaining', tab: 'motion-stat', component: MotionMostRemainingAircraft },
    { id: 'motion_longest_route', title: 'Longest Route', tab: 'motion-stat', component: MotionLongestRouteAircraft }
];
