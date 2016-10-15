eye = angular.module('eye', ['ngRoute']).
    filter('relDate', function() {
        return function(dstr) {
            return moment(dstr).fromNow();
        };
    }).
    filter('calDate', function() {
        return function(dstr) {
            return moment(dstr).calendar();
        };
    }).
    config(['$routeProvider', '$locationProvider',
            function($routeProvider, $locationProvider) {
                $locationProvider.html5Mode(true);
                $locationProvider.hashPrefix('!');

                $routeProvider.
                    when('/', {
                        templateUrl: '/static/partials/home.html',
                        controller: 'IndexCtrl'
                    }).
                    otherwise({
                        redirectTo: '/'
                    });
            }]);

function homeController($scope, $http) {
    $scope.recent = [];
    $scope.base = "https://storage.cloud.google.com/scenic-arc.appspot.com/basement";
    $http.get("//scenic-arc.appspot.com/api/recentImages").success(function(data) {
        $scope.recent = data;
    });
}

eye.controller('IndexCtrl', ['$scope', '$http', homeController]);
